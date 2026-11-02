// SPDX-License-Identifier: AGPL-3.0-or-later

package helper

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/crosswax/nasbuster/internal/sysexec"
)

type machineEnv struct {
	ops    *Ops
	run    *sysexec.Fake
	slept  time.Duration
	checks int // unix_chkpwd invocations
}

// newMachineEnv emulates a Raspberry Pi OS machine: "pi" is the account made
// in Imager (sudo group, password "raspberry"), "bob" a plain account, and
// "ghost" does not exist. unix_chkpwd answers as measured in the lab.
func newMachineEnv(t *testing.T) *machineEnv {
	t.Helper()
	e := &machineEnv{run: sysexec.NewFake()}
	e.run.Set(sysexec.Result{Stdout: []byte("pi adm dialout sudo video\n")}, "id", "-nG", "pi")
	e.run.Set(sysexec.Result{Stdout: []byte("bob users\n")}, "id", "-nG", "bob")
	e.run.Set(sysexec.Result{ExitCode: 1, Stderr: []byte("id: 'ghost': no such user")}, "id", "-nG", "ghost")
	e.run.HandleInput("unix_chkpwd", func(args []string, input []byte) (sysexec.Result, error) {
		e.checks++
		if len(args) != 2 || args[1] != "nonull" {
			t.Errorf("unix_chkpwd args = %q", args)
		}
		passwords := map[string]string{"pi": "raspberry", "root": "toor", "bob": "bobpass"}
		if pw, ok := passwords[args[0]]; ok && string(input) == pw+"\x00" {
			return sysexec.Result{}, nil
		}
		if _, ok := passwords[args[0]]; !ok {
			return sysexec.Result{ExitCode: 9}, nil // unknown account (PAM_AUTHINFO_UNAVAIL)
		}
		return sysexec.Result{ExitCode: 7}, nil // PAM_AUTH_ERR
	})
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	e.ops = &Ops{
		Run:   e.run,
		Now:   func() time.Time { return now },
		Sleep: func(d time.Duration) { e.slept += d },
	}
	return e
}

func TestVerifyMachineAccount(t *testing.T) {
	for _, tc := range []struct {
		name, user, password string
		ok                   bool
		wantChecks           int
	}{
		{"Imager account, right password", "pi", "raspberry", true, 1},
		{"root, right password", "root", "toor", true, 1},
		{"wrong password", "pi", "raspberri", false, 1},
		{"not an administrator", "bob", "bobpass", false, 0},
		{"unknown account", "ghost", "x", false, 0},
		{"name read as an option", "-n", "x", false, 0},
		{"NUL in the password", "pi", "raspberry\x00tail", false, 0},
		{"empty password", "pi", "", false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newMachineEnv(t)
			res, err := e.ops.verifyMachineAccount(context.Background(), MachineAccountArgs{User: tc.user, Password: tc.password})
			if tc.ok {
				if err != nil || !res.Verified {
					t.Fatalf("got %+v, %v; want verified", res, err)
				}
				if e.slept != 0 {
					t.Errorf("success waited %v", e.slept)
				}
			} else {
				if err == nil || err.Code != CodeMachineAccountDenied || res.Verified {
					t.Fatalf("got %+v, %v; want %s", res, err, CodeMachineAccountDenied)
				}
				// Same answer, same minimum duration, whatever the reason.
				if e.slept != machineCheckFloor {
					t.Errorf("refusal waited %v, want %v", e.slept, machineCheckFloor)
				}
			}
			if e.checks != tc.wantChecks {
				t.Errorf("unix_chkpwd ran %d times, want %d", e.checks, tc.wantChecks)
			}
		})
	}
}

func TestVerifyMachineAccountSendsPasswordOnStdinOnly(t *testing.T) {
	e := newMachineEnv(t)
	if _, err := e.ops.verifyMachineAccount(context.Background(), MachineAccountArgs{User: "pi", Password: "raspberry"}); err != nil {
		t.Fatal(err)
	}
	for i, call := range e.run.Calls() {
		for _, arg := range call {
			if arg == "raspberry" {
				t.Fatalf("password on a command line: %q", call)
			}
		}
		if call[0] == "unix_chkpwd" && string(e.run.Input(i)) != "raspberry\x00" {
			t.Fatalf("stdin = %q, want the password and a NUL", e.run.Input(i))
		}
	}
}

func TestDispatchMachineAccountRejectsUnknownFields(t *testing.T) {
	e := newMachineEnv(t)
	args, _ := json.Marshal(map[string]string{"user": "pi", "password": "raspberry", "group": "sudo"})
	if _, err := e.ops.Dispatch(context.Background(), Request{Op: OpMachineAccountVerify, Args: args}); err == nil {
		t.Fatal("unknown field accepted")
	}
	args, _ = json.Marshal(MachineAccountArgs{User: "pi", Password: "raspberry"})
	res, err := e.ops.Dispatch(context.Background(), Request{Op: OpMachineAccountVerify, Args: args})
	if err != nil || !res.(MachineAccountResult).Verified {
		t.Fatalf("dispatch = %+v, %v", res, err)
	}
}
