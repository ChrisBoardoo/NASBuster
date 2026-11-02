// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"net/url"
	"runtime/debug"
	"time"

	"github.com/crosswax/nasbuster/internal/auth"
)

// contentSecurityPolicy forbids every external origin and inline script.
// img-src data: is needed for the CSS grain texture (an SVG data URL).
const contentSecurityPolicy = "default-src 'self'; img-src 'self' data:; object-src 'none'; " +
	"base-uri 'none'; frame-ancestors 'none'; form-action 'self'"

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
		// No HSTS: with a self-signed certificate it would forbid the user from
		// accepting the browser warning.
		next.ServeHTTP(w, r)
	})
}

// apiGuard applies the API rules: bounded bodies, JSON-only writes, CSRF
// double-submit on state-changing methods, and a write deadline.
func (s *Server) apiGuard(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// SMART reads can take ~90 s behind slow USB bridges. SSE clears this.
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(2 * time.Minute))
		r.Body = http.MaxBytesReader(w, r.Body, maxBody)

		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if !sameOrigin(r) {
				writeError(w, http.StatusForbidden, "csrf.invalid", "Cross-origin request refused", nil)
				return
			}
			c, err := r.Cookie(csrfCookie)
			if err != nil || !auth.EqualTokens(r.Header.Get("X-CSRF-Token"), c.Value) {
				writeError(w, http.StatusForbidden, "csrf.invalid", "Missing or invalid CSRF token", nil)
				return
			}
			if r.ContentLength != 0 && !isJSON(r) {
				writeError(w, http.StatusUnsupportedMediaType, "request.unsupported_media_type", "Body must be application/json", nil)
				return
			}
		}
		next(w, r)
	})
}

// sameOrigin rejects requests whose Origin header names another host.
// Absent Origin (curl, old browsers) is left to the CSRF token check.
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host == r.Host
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach Flush and SetWriteDeadline.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		attrs := []any{"method", r.Method, "path", r.URL.Path, "status", rec.status,
			"duration", time.Since(start).Round(time.Millisecond), "remote", remoteIP(r)}
		if r.Method == http.MethodGet && rec.status < 400 {
			s.d.Logger.Debug("request", attrs...)
		} else {
			s.d.Logger.Info("request", attrs...)
		}
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				s.d.Logger.Error("panic", "path", r.URL.Path, "value", v, "stack", string(debug.Stack()))
				writeError(w, http.StatusInternalServerError, "internal.error", "Internal error", nil)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
