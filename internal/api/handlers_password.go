// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/crosswax/nasbuster/internal/auth"
	"github.com/crosswax/nasbuster/internal/store"
	"github.com/crosswax/nasbuster/internal/validate"
)

// A forgotten admin password (ADR-0037): changed once signed in, reset from
// the login screen with the machine account (ADR-0018), or by the console
// command (nasbusterd -reset-admin).

// settingMachineReset closes the login screen's reset door when false.
const settingMachineReset = "auth.machine_reset"

// resetGlobalKey limits reset attempts across all clients, apart from the
// login: an attack on the reset form never locks the ordinary sign-in.
const resetGlobalKey = "reset:*"

// machineResetAllowed reports whether the login screen may reset a password
// with the machine account. Open unless the admin closed it.
func (s *Server) machineResetAllowed(ctx context.Context) (bool, error) {
	allowed := true
	err := s.d.Store.GetSetting(ctx, settingMachineReset, &allowed)
	if errors.Is(err, store.ErrNotFound) {
		return true, nil
	}
	return allowed, err
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request, user store.User) {
	var body struct {
		CurrentPassword string `json:"currentPassword"`
		Password        string `json:"password"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	// A wrong current password counts as a failed sign-in.
	key := "login:" + remoteIP(r)
	if !s.allow(w, key) {
		return
	}
	ok := false
	if len(body.CurrentPassword) > 4*validate.PasswordMax {
		auth.VerifyDummy(body.CurrentPassword)
	} else {
		ok, _ = auth.VerifyPassword(body.CurrentPassword, user.PasswordHash)
	}
	if !ok {
		s.d.Limiter.Fail(key)
		s.audit(r, user.Username, "auth.password_change", user.Username, "denied", "wrong current password")
		writeError(w, http.StatusForbidden, "auth.wrong_password", "The current password is wrong", nil)
		return
	}
	if !validate.Password(body.Password) {
		writeError(w, http.StatusBadRequest, "validation.password_too_short", "Password too short",
			map[string]any{"min": validate.PasswordMin})
		return
	}
	hash, err := auth.HashPassword(body.Password)
	if err != nil {
		s.internalError(w, "hash password", err)
		return
	}
	if err := s.d.Store.SetUserPassword(r.Context(), user.ID, hash); err != nil {
		s.internalError(w, "set password", err)
		return
	}
	// Every other session of the account ends; this one stays.
	keep := ""
	if c, err := r.Cookie(sessionCookie); err == nil {
		keep = auth.HashToken(c.Value)
	}
	closed, err := s.d.Store.DeleteUserSessions(r.Context(), user.ID, keep)
	if err != nil {
		s.internalError(w, "close sessions", err)
		return
	}
	s.d.Limiter.Success(key)
	s.audit(r, user.Username, "auth.password_change", user.Username, "ok", fmt.Sprintf("%d other sessions closed", closed))
	writeJSON(w, http.StatusOK, map[string]any{"closedSessions": closed})
}

func (s *Server) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		MachineUser     string `json:"machineUser"`
		MachinePassword string `json:"machinePassword"`
		// Username picks the admin when there are several; with one, it may be empty.
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	key := "reset:" + remoteIP(r)
	if !s.allow(w, key) || !s.allow(w, resetGlobalKey) {
		return
	}
	ctx := r.Context()
	allowed, err := s.machineResetAllowed(ctx)
	if err != nil {
		s.internalError(w, "load auth settings", err)
		return
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "auth.reset_disabled", "Password reset from the login screen is turned off", nil)
		return
	}
	admins, err := s.d.Store.Admins(ctx)
	if err != nil {
		s.internalError(w, "list admins", err)
		return
	}
	if len(admins) == 0 {
		writeError(w, http.StatusConflict, "auth.setup_needed", "No administrator yet: finish the setup", nil)
		return
	}
	// The new password is checked first: it reveals nothing, and spares a
	// one-second machine check that would be wasted.
	if !validate.Password(body.Password) {
		writeError(w, http.StatusBadRequest, "validation.password_too_short", "Password too short",
			map[string]any{"min": validate.PasswordMin})
		return
	}
	if body.MachineUser == "" {
		writeError(w, http.StatusBadRequest, "auth.proof_required", "The machine account is required", nil)
		return
	}
	proof := machineProof{keys: []string{key, resetGlobalKey}, actor: "password-reset", action: "auth.password_reset", prefix: "auth"}
	if !s.verifyMachineAccount(w, r, proof, body.MachineUser, body.MachinePassword) {
		return
	}

	// Past the proof, the caller owns the machine: naming the admins reveals nothing.
	names := make([]string, len(admins))
	for i, a := range admins {
		names[i] = a.Username
	}
	var user store.User
	switch want := strings.ToLower(strings.TrimSpace(body.Username)); {
	case want != "":
		found := false
		for _, a := range admins {
			if a.Username == want {
				user, found = a, true
			}
		}
		if !found {
			writeError(w, http.StatusBadRequest, "auth.unknown_admin", "No administrator by that name", map[string]any{"admins": names})
			return
		}
	case len(admins) == 1:
		user = admins[0]
	default:
		writeError(w, http.StatusConflict, "auth.choose_admin", "Several administrators: say which one", map[string]any{"admins": names})
		return
	}

	hash, err := auth.HashPassword(body.Password)
	if err != nil {
		s.internalError(w, "hash password", err)
		return
	}
	if err := s.d.Store.SetUserPassword(ctx, user.ID, hash); err != nil {
		s.internalError(w, "set password", err)
		return
	}
	if _, err := s.d.Store.DeleteUserSessions(ctx, user.ID, ""); err != nil {
		s.internalError(w, "close sessions", err)
		return
	}
	s.d.Limiter.Success(key)
	if !s.startSession(w, r, user) {
		return
	}
	// The machine account name passed the helper's check: a valid Linux name.
	s.audit(r, user.Username, "auth.password_reset", user.Username, "ok", "machine account "+body.MachineUser)
	// An owner who asked for nothing learns of it.
	s.notify("auth.password_reset", map[string]any{"ip": remoteIP(r)})
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

// authSettings is GET/PUT /api/v1/auth/settings.
type authSettings struct {
	MachineReset bool `json:"machineReset"`
}

func (s *Server) handleGetAuthSettings(w http.ResponseWriter, r *http.Request, _ store.User) {
	allowed, err := s.machineResetAllowed(r.Context())
	if err != nil {
		s.internalError(w, "load auth settings", err)
		return
	}
	writeJSON(w, http.StatusOK, authSettings{MachineReset: allowed})
}

func (s *Server) handlePutAuthSettings(w http.ResponseWriter, r *http.Request, user store.User) {
	var body authSettings
	if !decodeJSON(w, r, &body) {
		return
	}
	if err := s.d.Store.SetSetting(r.Context(), settingMachineReset, body.MachineReset); err != nil {
		s.internalError(w, "save auth settings", err)
		return
	}
	state := "off"
	if body.MachineReset {
		state = "on"
	}
	s.audit(r, user.Username, "auth.settings", "", "ok", "machine reset "+state)
	writeJSON(w, http.StatusOK, body)
}

// notify sends a one-off notification (ntfy, webhook) in the background:
// the answer to the browser never waits for it. No-op without Deps.Notify.
func (s *Server) notify(code string, params map[string]any) {
	if s.d.Notify == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		s.d.Notify(ctx, code, params)
	}()
}
