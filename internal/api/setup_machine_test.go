// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/crosswax/nasbuster/internal/auth"
	"github.com/crosswax/nasbuster/internal/demo"
	"github.com/crosswax/nasbuster/internal/helper"
	"github.com/crosswax/nasbuster/internal/store"
)

// The first admin can prove ownership with the machine account instead of
// the setup code (ADR-0018). The demo Pi's account is pi / raspberry.
func TestSetupWithMachineAccount(t *testing.T) {
	e := newEnv(t)
	e.bootstrap()
	admin := func(machineUser, machinePassword string) map[string]string {
		return map[string]string{
			"machineUser": machineUser, "machinePassword": machinePassword,
			"username": "admin", "password": "correct horse battery", "language": "en",
		}
	}

	// Wrong password, unknown account: the same answer, nothing created.
	for _, bad := range [][2]string{{demo.MachineUser, "raspberri"}, {"ghost", "x"}, {"-n", "x"}} {
		if c := errCode(t, e, "POST", "/api/v1/setup/admin", admin(bad[0], bad[1]), http.StatusForbidden); c != "setup.invalid_machine_account" {
			t.Fatalf("%q: %s", bad[0], c)
		}
	}
	// No proof at all.
	if c := errCode(t, e, "POST", "/api/v1/setup/admin", map[string]string{"username": "admin", "password": "correct horse battery"}, http.StatusBadRequest); c != "setup.proof_required" {
		t.Fatalf("no proof: %s", c)
	}

	e.do("POST", "/api/v1/setup/admin", admin(demo.MachineUser, demo.MachinePassword), http.StatusCreated, nil)
	boot := e.bootstrap()
	if boot["needsSetup"] != false || boot["authenticated"] != true {
		t.Fatalf("after setup bootstrap = %v", boot)
	}
	// The door closes: neither proof works once an admin exists.
	if c := errCode(t, e, "POST", "/api/v1/setup/admin", admin(demo.MachineUser, demo.MachinePassword), http.StatusConflict); c != "setup.already_done" {
		t.Fatalf("second setup: %s", c)
	}

	// The audit trail records the refusals, never a password.
	var audit struct{ Entries []store.AuditEntry }
	e.do("GET", "/api/v1/audit", nil, http.StatusOK, &audit)
	denied := 0
	for _, en := range audit.Entries {
		if strings.Contains(en.Detail, "raspberr") || strings.Contains(en.Target, "raspberr") {
			t.Fatalf("password in the audit trail: %+v", en)
		}
		if en.Action == "setup.admin" && en.Result == "denied" {
			denied++
		}
	}
	if denied != 3 {
		t.Errorf("denied entries = %d, want 3", denied)
	}
}

func TestMachineAccountFailuresShareTheSetupLimit(t *testing.T) {
	e := newEnv(t)
	e.bootstrap()
	for i := 0; i < 6; i++ { // 5 free failures, the 6th locks
		errCode(t, e, "POST", "/api/v1/setup/admin", map[string]string{
			"machineUser": demo.MachineUser, "machinePassword": "guess", "username": "admin", "password": "correct horse battery",
		}, http.StatusForbidden)
	}
	// Locked: even the right setup code must wait.
	if c := errCode(t, e, "POST", "/api/v1/setup/admin", map[string]string{
		"setupCode": testCode, "username": "admin", "password": "correct horse battery",
	}, http.StatusTooManyRequests); c != "auth.rate_limited" {
		t.Fatalf("after 6 machine failures: %s", c)
	}
}

type downHelper struct{}

func (downHelper) Call(context.Context, string, any, any) error { return helper.ErrUnavailable }

func TestMachineAccountCheckWithoutHelper(t *testing.T) {
	limiter := auth.NewLimiter()
	s := &Server{d: Deps{Helper: downHelper{}, Limiter: limiter, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}}
	rec := httptest.NewRecorder()
	if s.verifyMachineAccount(rec, httptest.NewRequest("POST", "/api/v1/setup/admin", nil), setupProof("setup:192.0.2.1"), "pi", "raspberry") {
		t.Fatal("accepted without a helper")
	}
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "setup.machine_check_unavailable") {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
	// Not the user's fault: nothing counted against the limits.
	for i := 0; i < 10; i++ {
		if ok, _ := limiter.Allow("setup:192.0.2.1"); !ok {
			t.Fatal("helper outage counted as a failed guess")
		}
	}
}
