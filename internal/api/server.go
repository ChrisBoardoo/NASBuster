// SPDX-License-Identifier: AGPL-3.0-or-later

// Package api serves the HTTP API (see md/8.architecture/api-v1.md) and the
// embedded web UI.
package api

import (
	"context"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/crosswax/nasbuster/internal/auth"
	"github.com/crosswax/nasbuster/internal/helper"
	"github.com/crosswax/nasbuster/internal/metrics"
	"github.com/crosswax/nasbuster/internal/storage"
	"github.com/crosswax/nasbuster/internal/store"
	"github.com/crosswax/nasbuster/internal/system"
	"github.com/crosswax/nasbuster/internal/tlsutil"
)

// Cookie names and lifetimes.
const (
	sessionCookie = "cwnsb_session"
	csrfCookie    = "cwnsb_csrf"
	sessionTTL    = 7 * 24 * time.Hour
	maxBody       = 64 << 10
)

// DiskSource lists disks (storage.Discoverer in production).
type DiskSource interface {
	Disks(ctx context.Context) ([]storage.Disk, error)
	Disk(ctx context.Context, id string) (storage.Disk, error)
}

// SetupCode exposes the first-run code.
type SetupCode interface {
	Current() string // "" once setup is done
	Consume()        // called right after the first admin is created
}

// Deps are the server's collaborators.
type Deps struct {
	Store     *store.Store
	Prober    *system.Prober
	Disks     DiskSource
	Helper    helper.Client
	Metrics   *metrics.Sampler
	Limiter   *auth.Limiter
	SetupCode SetupCode
	UI        fs.FS // built web UI (web/dist); may be empty
	Logger    *slog.Logger
	Version   string
	// SecureCookies sets the Secure flag (true whenever serving HTTPS).
	SecureCookies bool
	Demo          bool
	// DemoFault simulates a disk event (demo mode only; nil otherwise).
	DemoFault func(kind, disk string) error
	// ExportPath receives the database copy for configuration backups (ADR-0014).
	ExportPath string
	// Restart asks the daemon to restart (to load a restored database); nil: not supported.
	Restart func()
	LabMode bool
	// Certificates manages the HTTPS certificate (ADR-0019); nil without TLS.
	Certificates Certificates
	// AdminCheck asks a local application (Portainer) whether its admin
	// account exists; nil: apps.AdminExists.
	AdminCheck func(ctx context.Context, scheme string, port int) (bool, error)
	// Now is the clock (nil: time.Now; the demo and tests drive their own).
	Now func() time.Time
	// Boot gives the current boot id (a parity repair does not survive a
	// reboot); nil: unknown.
	Boot func() (id string, uptime time.Duration, ok bool)
	// Notify sends a one-off notification (ntfy, webhook) titled by its alert
	// code; nil: none (demo, tests).
	Notify func(ctx context.Context, code string, params map[string]any)
}

func (s *Server) bootID() string {
	if s.d.Boot == nil {
		return ""
	}
	id, _, _ := s.d.Boot()
	return id
}

// Certificates is the HTTPS certificate manager (tlsutil.Manager).
type Certificates interface {
	Info() tlsutil.Info
	Covers(hostname string) bool
	Renew(hostname string) error
}

// Server is the HTTP handler.
type Server struct {
	d   Deps
	mux *http.ServeMux
}

// New builds the handler.
func New(d Deps) http.Handler {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.Limiter == nil {
		d.Limiter = auth.NewLimiter()
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	s := &Server{d: d, mux: http.NewServeMux()}
	s.routes()
	return s.recoverer(s.logRequests(securityHeaders(s.mux)))
}

func (s *Server) routes() {
	// Public.
	s.public("GET /api/v1/health", s.handleHealth)
	s.public("GET /api/v1/bootstrap", s.handleBootstrap)
	s.public("GET /api/v1/setup/checks", s.handleSetupChecks)
	s.public("POST /api/v1/setup/admin", s.handleSetupAdmin)
	s.public("POST /api/v1/auth/login", s.handleLogin)
	s.public("POST /api/v1/auth/reset-password", s.handleResetPassword) // ADR-0037

	// Authenticated.
	s.authed("POST /api/v1/auth/logout", s.handleLogout)
	s.authed("GET /api/v1/auth/me", s.handleMe)
	s.authed("PATCH /api/v1/auth/me", s.handlePatchMe)
	s.authed("PUT /api/v1/auth/me/password", s.handleChangePassword)
	s.authed("GET /api/v1/auth/settings", s.handleGetAuthSettings)
	s.authed("PUT /api/v1/auth/settings", s.handlePutAuthSettings)
	s.authed("GET /api/v1/onboarding", s.handleGetOnboarding)
	s.authed("PUT /api/v1/onboarding", s.handlePutOnboarding)
	s.authed("GET /api/v1/system/info", s.handleSystemInfo)
	s.authed("GET /api/v1/system/checks", s.handleSystemChecks)
	s.authed("GET /api/v1/system/metrics", s.handleMetrics)
	s.authed("GET /api/v1/system/timezones", s.handleTimezones)
	s.authed("PUT /api/v1/system/identity", s.handleIdentity)
	s.authed("GET /api/v1/system/certificate", s.handleCertificate)
	s.authed("POST /api/v1/system/certificate/renew", s.handleCertificateRenew)
	s.authed("GET /api/v1/audit", s.handleAudit)
	s.authed("GET /api/v1/storage/disks", s.handleDisks)
	s.authed("GET /api/v1/storage/disks/{id}/smart", s.handleSmart)
	s.authed("POST /api/v1/storage/disks/{id}/wipe", s.handleWipeDisk)
	s.authed("POST /api/v1/storage/disks/{id}/locate", s.handleLocateDisk)
	s.authed("GET /api/v1/events", s.handleEvents)
	s.authed("GET /api/v1/storage/layouts", s.handleLayouts)
	s.authed("POST /api/v1/storage/pools/plan", s.handlePoolPlan)
	s.authed("GET /api/v1/storage/pools/importable", s.handleImportable)
	s.authed("GET /api/v1/system/config-backups", s.handleConfigBackups)
	s.authed("GET /api/v1/import/sources", s.handleImportSources)
	s.authed("POST /api/v1/import/mount", s.handleImportMount)
	s.authed("POST /api/v1/import/unmount", s.handleImportUnmount)
	s.authed("POST /api/v1/import/copy", s.handleImportCopy)
	s.authed("GET /api/v1/import/progress/{share}", s.handleImportProgress)
	s.authed("POST /api/v1/system/config-backup", s.handleConfigBackup)
	s.authed("POST /api/v1/system/config-restore", s.handleConfigRestore)
	s.authed("POST /api/v1/storage/pools/import", s.handleImportPool)
	s.authed("GET /api/v1/storage/pools", s.handlePools)
	s.authed("POST /api/v1/storage/pools", s.handleCreatePool)
	s.authed("GET /api/v1/storage/pools/{name}", s.handlePool)
	s.authed("POST /api/v1/storage/pools/{name}/scrub", s.handleScrub)
	s.authed("POST /api/v1/storage/pools/{name}/scrub/cancel", s.handleScrubCancel)
	s.authed("POST /api/v1/storage/pools/{name}/scrub/resume", s.handleScrubResume)
	s.authed("DELETE /api/v1/storage/pools/{name}", s.handleDestroyPool)
	s.authed("POST /api/v1/storage/pools/{name}/mount-degraded", s.handleMountDegraded)
	s.authed("POST /api/v1/storage/pools/{name}/replace", s.handleReplaceDisk)
	s.authed("POST /api/v1/storage/pools/{name}/restore-redundancy", s.handleRestoreRedundancy)
	s.authed("POST /api/v1/storage/pools/{name}/clear-errors", s.handleClearErrors)
	s.authed("POST /api/v1/storage/pools/{name}/resume", s.handleResume)
	s.authed("POST /api/v1/storage/pools/{name}/reassemble", s.handleReassemble)
	if s.d.DemoFault != nil {
		s.authed("POST /api/v1/demo/faults", s.handleDemoFault) // demo mode only
	}
	s.authed("GET /api/v1/users", s.handleUsers)
	s.authed("POST /api/v1/users", s.handleCreateUser)
	s.authed("PUT /api/v1/users/{name}/password", s.handleUserPassword)
	s.authed("DELETE /api/v1/users/{name}", s.handleDeleteUser)
	s.authed("GET /api/v1/shares", s.handleShares)
	s.authed("POST /api/v1/shares", s.handleCreateShare)
	s.authed("PUT /api/v1/shares/{name}/access", s.handleShareAccess)
	s.authed("DELETE /api/v1/shares/{name}", s.handleDeleteShare)
	s.authed("PUT /api/v1/shares/{name}/timemachine", s.handleShareTimeMachine)
	s.authed("PUT /api/v1/shares/{name}/recycle", s.handleShareRecycle)
	s.authed("GET /api/v1/shares/sessions", s.handleSessions)
	s.authed("GET /api/v1/shares/{name}/snapshots", s.handleSnapshots)
	s.authed("POST /api/v1/shares/{name}/snapshots", s.handleCreateSnapshot)
	s.authed("DELETE /api/v1/shares/{name}/snapshots/{snapshot}", s.handleDeleteSnapshot)
	s.authed("GET /api/v1/protection", s.handleGetProtection)
	s.authed("PUT /api/v1/protection", s.handlePutProtection)
	s.authed("GET /api/v1/apps", s.handleApps)
	s.authed("POST /api/v1/apps", s.handleCreateApp)
	s.authed("GET /api/v1/apps/engine", s.handleEngine)
	s.authed("POST /api/v1/apps/engine/install", s.handleEngineInstall)
	s.authed("POST /api/v1/apps/engine/remove", s.handleEngineRemove)
	s.authed("GET /api/v1/apps/catalog", s.handleCatalog)
	s.authed("POST /api/v1/apps/orphans/{name}/{action}", s.handleOrphanAction)
	s.authed("GET /api/v1/jobs/{job}", s.handleJob)
	s.authed("PUT /api/v1/apps/{name}", s.handleUpdateApp)
	s.authed("DELETE /api/v1/apps/{name}", s.handleDeleteApp)
	s.authed("POST /api/v1/apps/{name}/{action}", s.handleAppAction)
	s.authed("GET /api/v1/apps/{name}/logs", s.handleAppLogs)
	s.authed("GET /api/v1/apps/{name}/setup-token", s.handleAppSetupToken)
	s.authed("GET /api/v1/alerts", s.handleAlerts)
	s.authed("GET /api/v1/journal", s.handleJournal)
	s.authed("GET /api/v1/journal/settings", s.handleGetJournalSettings)
	s.authed("PUT /api/v1/journal/settings", s.handlePutJournalSettings)
	s.authed("POST /api/v1/alerts/{id}/ack", s.handleAckAlert)
	s.authed("GET /api/v1/notifications", s.handleGetNotifications)
	s.authed("PUT /api/v1/notifications", s.handlePutNotifications)
	s.authed("POST /api/v1/notifications/test", s.handleTestNotifications)

	s.mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "request.not_found", "Unknown API route", nil)
	})
	s.mux.Handle("/", s.spa())
}

// public registers an API route that needs no session.
func (s *Server) public(pattern string, h http.HandlerFunc) {
	s.mux.Handle(pattern, s.apiGuard(h))
}

// authed registers an API route that requires a valid session.
func (s *Server) authed(pattern string, h func(http.ResponseWriter, *http.Request, store.User)) {
	s.mux.Handle(pattern, s.apiGuard(func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.currentUser(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "auth.required", "Authentication required", nil)
			return
		}
		h(w, r, user)
	}))
}

// currentUser resolves the session cookie.
func (s *Server) currentUser(r *http.Request) (store.User, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" || len(c.Value) > 128 {
		return store.User{}, false
	}
	u, err := s.d.Store.SessionUser(r.Context(), auth.HashToken(c.Value))
	return u, err == nil
}

func (s *Server) audit(r *http.Request, actor, action, target, result, detail string) {
	err := s.d.Store.Audit(r.Context(), store.AuditEntry{
		Actor: actor, Action: action, Target: target, Result: result, Detail: detail, RemoteAddr: remoteIP(r),
	})
	if err != nil {
		s.d.Logger.Error("audit write failed", "action", action, "err", err)
	}
}

// remoteIP is the TCP peer address. No proxy headers are trusted.
func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) setCookie(w http.ResponseWriter, name, value string, maxAge time.Duration, httpOnly bool) {
	c := &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		HttpOnly: httpOnly,
		Secure:   s.d.SecureCookies,
		SameSite: http.SameSiteStrictMode,
	}
	if maxAge < 0 {
		c.MaxAge = -1
	} else if maxAge > 0 {
		c.MaxAge = int(maxAge / time.Second)
	}
	http.SetCookie(w, c)
}
