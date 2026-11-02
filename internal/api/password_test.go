// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
	"time"

	"github.com/crosswax/nasbuster/internal/demo"
	"github.com/crosswax/nasbuster/internal/store"
)

const newPassword = "a brand new passphrase"

// otherBrowser signs the admin in from a second browser (its own cookies).
func (e *env) otherBrowser(password string) *env {
	e.t.Helper()
	jar, _ := cookiejar.New(nil)
	other := &env{t: e.t, sys: e.sys, h: e.h, srv: e.srv, client: &http.Client{Jar: jar, Timeout: 60 * time.Second}}
	other.bootstrap()
	other.do("POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": password}, http.StatusOK, nil)
	return other
}

// Changing one's password (ADR-0037): the current one is asked, the other
// sessions end, this one stays.
func TestChangePassword(t *testing.T) {
	e := newEnv(t)
	e.setup()
	other := e.otherBrowser("correct horse battery")

	if c := errCode(t, e, "PUT", "/api/v1/auth/me/password", map[string]string{"currentPassword": "wrong", "password": newPassword}, http.StatusForbidden); c != "auth.wrong_password" {
		t.Fatalf("wrong current password: %s", c)
	}
	if c := errCode(t, e, "PUT", "/api/v1/auth/me/password", map[string]string{"currentPassword": "correct horse battery", "password": "short"}, http.StatusBadRequest); c != "validation.password_too_short" {
		t.Fatalf("short password: %s", c)
	}
	var out struct{ ClosedSessions int }
	e.do("PUT", "/api/v1/auth/me/password", map[string]string{"currentPassword": "correct horse battery", "password": newPassword}, http.StatusOK, &out)
	if out.ClosedSessions != 1 {
		t.Errorf("closed sessions = %d, want 1", out.ClosedSessions)
	}
	e.do("GET", "/api/v1/auth/me", nil, http.StatusOK, nil)
	if c := errCode(t, other, "GET", "/api/v1/auth/me", nil, http.StatusUnauthorized); c != "auth.required" {
		t.Fatalf("other browser still signed in: %s", c)
	}
	other.do("POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": "correct horse battery"}, http.StatusUnauthorized, nil)
	other.do("POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": newPassword}, http.StatusOK, nil)
}

// A forgotten password is reset with the machine account (ADR-0037): the
// same proof as for the first admin, a door of its own, every session closed.
func TestResetPasswordWithMachineAccount(t *testing.T) {
	notified := make(chan string, 4)
	e := newEnvWith(t, func(d *Deps) {
		d.Notify = func(_ context.Context, code string, params map[string]any) {
			notified <- code + " " + params["ip"].(string)
		}
	})
	reset := func(machineUser, machinePassword, username, password string) map[string]string {
		return map[string]string{"machineUser": machineUser, "machinePassword": machinePassword, "username": username, "password": password}
	}
	good := reset(demo.MachineUser, demo.MachinePassword, "", newPassword)

	// Before the first admin, there is nothing to reset: the setup does it.
	if boot := e.bootstrap(); boot["machineReset"] != nil {
		t.Fatalf("machineReset before setup: %v", boot["machineReset"])
	}
	if c := errCode(t, e, "POST", "/api/v1/auth/reset-password", good, http.StatusConflict); c != "auth.setup_needed" {
		t.Fatalf("before setup: %s", c)
	}

	e.setup()
	other := e.otherBrowser("correct horse battery")
	e.do("POST", "/api/v1/auth/logout", nil, http.StatusNoContent, nil)
	if boot := e.bootstrap(); boot["machineReset"] != true {
		t.Fatalf("machineReset = %v, want true (open by default)", boot["machineReset"])
	}

	for _, tc := range []struct {
		body   map[string]string
		status int
		code   string
	}{
		{reset(demo.MachineUser, "raspberri", "", newPassword), http.StatusForbidden, "auth.invalid_machine_account"},
		{reset("ghost", "x", "", newPassword), http.StatusForbidden, "auth.invalid_machine_account"},
		{reset(demo.MachineUser, demo.MachinePassword, "", "short"), http.StatusBadRequest, "validation.password_too_short"},
		{reset("", "", "", newPassword), http.StatusBadRequest, "auth.proof_required"},
		{reset(demo.MachineUser, demo.MachinePassword, "nobody", newPassword), http.StatusBadRequest, "auth.unknown_admin"},
	} {
		if c := errCode(t, e, "POST", "/api/v1/auth/reset-password", tc.body, tc.status); c != tc.code {
			t.Fatalf("%v: %s, want %s", tc.body, c, tc.code)
		}
	}
	e.do("POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": "correct horse battery"}, http.StatusOK, nil)
	e.do("POST", "/api/v1/auth/logout", nil, http.StatusNoContent, nil)

	var out struct{ User store.User }
	e.do("POST", "/api/v1/auth/reset-password", good, http.StatusOK, &out)
	if out.User.Username != "admin" {
		t.Fatalf("reset user = %+v", out.User)
	}
	e.do("GET", "/api/v1/auth/me", nil, http.StatusOK, nil) // signed in at once
	if c := errCode(t, other, "GET", "/api/v1/auth/me", nil, http.StatusUnauthorized); c != "auth.required" {
		t.Fatalf("old session survived the reset: %s", c)
	}
	other.do("POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": newPassword}, http.StatusOK, nil)
	select {
	case got := <-notified:
		if !strings.HasPrefix(got, "auth.password_reset ") {
			t.Errorf("notification = %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no notification after the reset")
	}

	// The audit trail names the machine account, never a password.
	var audit struct{ Entries []store.AuditEntry }
	e.do("GET", "/api/v1/audit", nil, http.StatusOK, &audit)
	ok, denied := 0, 0
	for _, en := range audit.Entries {
		if strings.Contains(en.Detail+en.Target, "raspberr") || strings.Contains(en.Detail+en.Target, "passphrase") {
			t.Fatalf("password in the audit trail: %+v", en)
		}
		if en.Action == "auth.password_reset" {
			switch en.Result {
			case "ok":
				ok++
				if en.Detail != "machine account "+demo.MachineUser {
					t.Errorf("detail = %q", en.Detail)
				}
			case "denied":
				denied++
			}
		}
	}
	if ok != 1 || denied != 2 {
		t.Errorf("audit: %d ok, %d denied, want 1 and 2", ok, denied)
	}

	// The admin can close the door: only the console command is left.
	var settings authSettings
	e.do("PUT", "/api/v1/auth/settings", authSettings{MachineReset: false}, http.StatusOK, &settings)
	e.do("GET", "/api/v1/auth/settings", nil, http.StatusOK, &settings)
	if settings.MachineReset {
		t.Fatal("door still open")
	}
	e.do("POST", "/api/v1/auth/logout", nil, http.StatusNoContent, nil)
	if boot := e.bootstrap(); boot["machineReset"] != false {
		t.Fatalf("machineReset = %v, want false", boot["machineReset"])
	}
	if c := errCode(t, e, "POST", "/api/v1/auth/reset-password", good, http.StatusForbidden); c != "auth.reset_disabled" {
		t.Fatalf("closed door: %s", c)
	}
}

// Guessing at the reset form locks the form, never the ordinary sign-in.
func TestResetPasswordHasItsOwnLimit(t *testing.T) {
	e := newEnv(t)
	e.setup()
	e.do("POST", "/api/v1/auth/logout", nil, http.StatusNoContent, nil)
	guess := map[string]string{"machineUser": demo.MachineUser, "machinePassword": "guess", "password": newPassword}
	for i := 0; i < 6; i++ { // 5 free failures, the 6th locks
		errCode(t, e, "POST", "/api/v1/auth/reset-password", guess, http.StatusForbidden)
	}
	if c := errCode(t, e, "POST", "/api/v1/auth/reset-password", guess, http.StatusTooManyRequests); c != "auth.rate_limited" {
		t.Fatalf("after 6 guesses: %s", c)
	}
	e.do("POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": "correct horse battery"}, http.StatusOK, nil)
}
