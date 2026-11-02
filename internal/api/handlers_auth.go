// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/crosswax/nasbuster/internal/auth"
	"github.com/crosswax/nasbuster/internal/helper"
	"github.com/crosswax/nasbuster/internal/store"
	"github.com/crosswax/nasbuster/internal/validate"
)

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": s.d.Version})
}

func (s *Server) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(csrfCookie); err != nil || len(c.Value) < 32 {
		s.setCookie(w, csrfCookie, auth.NewToken(), 30*24*time.Hour, false)
	}
	n, err := s.d.Store.UserCount(r.Context())
	if err != nil {
		s.internalError(w, "count users", err)
		return
	}
	resp := map[string]any{
		"version":       s.d.Version,
		"needsSetup":    n == 0,
		"authenticated": false,
		"user":          nil,
		"hostname":      s.d.Prober.Info().Hostname,
		"demo":          s.d.Demo,
		"labMode":       s.d.LabMode,
	}
	if u, ok := s.currentUser(r); ok {
		resp["authenticated"] = true
		resp["user"] = u
	}
	if n > 0 {
		// The login screen offers "Forgot password?" or the console command (ADR-0037).
		allowed, err := s.machineResetAllowed(r.Context())
		if err != nil {
			s.internalError(w, "load auth settings", err)
			return
		}
		resp["machineReset"] = allowed
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleSetupChecks(w http.ResponseWriter, r *http.Request) {
	if !s.needsSetup(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"checks": s.d.Prober.Checks(r.Context())})
}

// needsSetup writes 403 setup.already_done and returns false once an admin exists.
func (s *Server) needsSetup(w http.ResponseWriter, r *http.Request) bool {
	n, err := s.d.Store.UserCount(r.Context())
	if err != nil {
		s.internalError(w, "count users", err)
		return false
	}
	if n > 0 {
		writeError(w, http.StatusForbidden, "setup.already_done", "Setup is already done", nil)
		return false
	}
	return true
}

// setupGlobalKey limits setup-code attempts across all clients.
const setupGlobalKey = "setup:*"

func (s *Server) handleSetupAdmin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		// Proof of ownership: the machine account (ADR-0018), or the setup code.
		MachineUser     string `json:"machineUser"`
		MachinePassword string `json:"machinePassword"`
		SetupCode       string `json:"setupCode"`
		// The new NAS administrator.
		Username string `json:"username"`
		Password string `json:"password"`
		Language string `json:"language"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	// Per-IP and global limits: many LAN addresses (IPv6 privacy addresses)
	// must not multiply the guessing rate of the setup code.
	key := "setup:" + remoteIP(r)
	if !s.allow(w, key) || !s.allow(w, setupGlobalKey) {
		return
	}
	n, err := s.d.Store.UserCount(r.Context())
	if err != nil {
		s.internalError(w, "count users", err)
		return
	}
	if n > 0 {
		writeError(w, http.StatusConflict, "setup.already_done", "Setup is already done", nil)
		return
	}
	// The proof of ownership is checked before anything else can leak information.
	switch {
	case body.MachineUser != "":
		if !s.verifyMachineAccount(w, r, setupProof(key), body.MachineUser, body.MachinePassword) {
			return
		}
	case body.SetupCode != "":
		if !auth.SetupCodeMatches(body.SetupCode, s.d.SetupCode.Current()) {
			s.d.Limiter.Fail(key)
			s.d.Limiter.Fail(setupGlobalKey)
			s.audit(r, "-", "setup.admin", "", "denied", "invalid setup code")
			writeError(w, http.StatusForbidden, "setup.invalid_code", "Invalid setup code", nil)
			return
		}
	default:
		writeError(w, http.StatusBadRequest, "setup.proof_required", "A machine account or the setup code is required", nil)
		return
	}
	body.Username = strings.TrimSpace(body.Username)
	if !validate.Username(body.Username) {
		writeError(w, http.StatusBadRequest, "validation.username", "Invalid username", nil)
		return
	}
	if !validate.Password(body.Password) {
		writeError(w, http.StatusBadRequest, "validation.password_too_short", "Password too short",
			map[string]any{"min": validate.PasswordMin})
		return
	}
	if body.Language == "" {
		body.Language = "en"
	}
	if !validate.Language(body.Language) {
		writeError(w, http.StatusBadRequest, "validation.language", "Unsupported language", nil)
		return
	}
	hash, err := auth.HashPassword(body.Password)
	if err != nil {
		s.internalError(w, "hash password", err)
		return
	}
	user, err := s.d.Store.CreateFirstAdmin(r.Context(), body.Username, hash, body.Language)
	if errors.Is(err, store.ErrSetupDone) {
		writeError(w, http.StatusConflict, "setup.already_done", "Setup is already done", nil)
		return
	}
	if err != nil {
		s.internalError(w, "create admin", err)
		return
	}
	s.d.Limiter.Success(key)
	s.d.Limiter.Success(setupGlobalKey)
	s.d.SetupCode.Consume()
	if !s.startSession(w, r, user) {
		return
	}
	s.audit(r, user.Username, "setup.admin", user.Username, "ok", "")
	writeJSON(w, http.StatusCreated, map[string]any{"user": user})
}

// verifyMachineAccount asks the helper whether user and password belong to an
// administrator of this machine (ADR-0018). Refusals count against the setup
// rate limits, like a wrong setup code. It writes the error response itself
// and reports whether the account was accepted.
// machineProof says how a machine-account check (ADR-0018) is counted and
// reported: for the first admin, or for a forgotten password (ADR-0037).
type machineProof struct {
	keys   []string // limiter keys charged on a refusal
	actor  string   // helper actor
	action string   // audit action
	prefix string   // error code prefix: "setup" or "auth"
}

func setupProof(key string) machineProof {
	return machineProof{keys: []string{key, setupGlobalKey}, actor: "setup", action: "setup.admin", prefix: "setup"}
}

func (s *Server) verifyMachineAccount(w http.ResponseWriter, r *http.Request, p machineProof, user, password string) bool {
	ctx := helper.WithActor(r.Context(), p.actor)
	err := s.d.Helper.Call(ctx, helper.OpMachineAccountVerify, helper.MachineAccountArgs{User: user, Password: password}, nil)
	var he *helper.Error
	switch {
	case err == nil:
		return true
	case errors.As(err, &he) && he.Code == helper.CodeMachineAccountDenied:
		for _, key := range p.keys {
			s.d.Limiter.Fail(key)
		}
		s.audit(r, "-", p.action, "", "denied", "invalid machine account")
		writeError(w, http.StatusForbidden, p.prefix+".invalid_machine_account", "Invalid machine account", nil)
	default:
		// The helper is down or failed: not the user's fault, nothing counted.
		// The setup code (or the console command) still works without the helper.
		s.d.Logger.Warn("machine account check unavailable", "err", err)
		writeError(w, http.StatusServiceUnavailable, p.prefix+".machine_check_unavailable", "The machine account cannot be checked right now", nil)
	}
	return false
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	key := "login:" + remoteIP(r)
	if !s.allow(w, key) {
		return
	}
	username := strings.ToLower(strings.TrimSpace(body.Username))
	user, err := s.d.Store.UserByUsername(r.Context(), username)
	ok := false
	switch {
	case errors.Is(err, store.ErrNotFound) || len(body.Password) > 4*validate.PasswordMax:
		auth.VerifyDummy(body.Password) // same timing as a real check
	case err != nil:
		s.internalError(w, "load user", err)
		return
	default:
		ok, _ = auth.VerifyPassword(body.Password, user.PasswordHash)
	}
	if !ok {
		s.d.Limiter.Fail(key)
		// Only log names that look like usernames: people sometimes type their
		// password in the username field, and it must not land in the audit log.
		actor := "-"
		if validate.Username(username) {
			actor = username
		}
		s.audit(r, actor, "auth.login", "", "denied", "")
		writeError(w, http.StatusUnauthorized, "auth.invalid_credentials", "Invalid username or password", nil)
		return
	}
	s.d.Limiter.Success(key)
	if !s.startSession(w, r, user) {
		return
	}
	s.audit(r, user.Username, "auth.login", "", "ok", "")
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

// allow enforces the rate limiter, writing 429 when locked.
func (s *Server) allow(w http.ResponseWriter, key string) bool {
	ok, wait := s.d.Limiter.Allow(key)
	if !ok {
		secs := int(math.Ceil(wait.Seconds()))
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		writeError(w, http.StatusTooManyRequests, "auth.rate_limited", "Too many attempts", map[string]any{"retryAfter": secs})
	}
	return ok
}

// startSession issues a fresh session token (never reusing one sent by the client).
func (s *Server) startSession(w http.ResponseWriter, r *http.Request, user store.User) bool {
	token := auth.NewToken()
	if _, err := s.d.Store.CreateSession(r.Context(), auth.HashToken(token), user.ID, sessionTTL, remoteIP(r), r.UserAgent()); err != nil {
		s.internalError(w, "create session", err)
		return false
	}
	s.setCookie(w, sessionCookie, token, sessionTTL, true)
	return true
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request, user store.User) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		_ = s.d.Store.DeleteSession(r.Context(), auth.HashToken(c.Value))
	}
	s.setCookie(w, sessionCookie, "", -1, true)
	s.audit(r, user.Username, "auth.logout", "", "ok", "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request, user store.User) {
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (s *Server) handlePatchMe(w http.ResponseWriter, r *http.Request, user store.User) {
	var body struct {
		Language string `json:"language"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if !validate.Language(body.Language) {
		writeError(w, http.StatusBadRequest, "validation.language", "Unsupported language", nil)
		return
	}
	if err := s.d.Store.SetUserLanguage(r.Context(), user.ID, body.Language); err != nil {
		s.internalError(w, "set language", err)
		return
	}
	user.Language = body.Language
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (s *Server) internalError(w http.ResponseWriter, what string, err error) {
	s.d.Logger.Error(what, "err", err)
	writeError(w, http.StatusInternalServerError, "internal.error", "Internal error", nil)
}
