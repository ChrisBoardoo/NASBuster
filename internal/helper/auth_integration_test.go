// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build integration && linux

// Real machine-account checks in the cwnsb-lab distro (ADR-0018): real
// accounts in /etc/shadow (yescrypt on Debian 13), real id, real unix_chkpwd.
package helper

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestLabVerifyMachineAccount(t *testing.T) {
	host, _ := os.Hostname()
	disposable := host == "cwnsb-lab" || os.Getenv("GITHUB_ACTIONS") == "true"
	if os.Getenv("CWNSB_LAB") != "1" || !disposable || os.Geteuid() != 0 {
		t.Skip("integration tests run as root with CWNSB_LAB=1, in the cwnsb-lab distro or a CI runner")
	}
	const admin, plain = "cwnsb-it-admin", "cwnsb-it-plain"
	const password = `Tr0ub4dor & "3" é $HOME`
	run := func(input string, name string, args ...string) {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Stdin = strings.NewReader(input)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
	}
	for _, u := range []string{admin, plain} {
		_ = exec.Command("userdel", "-r", u).Run()
		run("", "useradd", "--create-home", "--shell", "/bin/bash", u)
		run(u+":"+password+"\n", "chpasswd")
		t.Cleanup(func() { _ = exec.Command("userdel", "-r", u).Run() })
	}
	run("", "usermod", "-aG", "sudo", admin)

	ops := NewOps(quietLogger(), "")
	ctx := context.Background()
	if res, err := ops.verifyMachineAccount(ctx, MachineAccountArgs{User: admin, Password: password}); err != nil || !res.Verified {
		t.Fatalf("administrator with the right password: %+v, %v", res, err)
	}

	for _, tc := range []struct{ name, user, password string }{
		{"wrong password", admin, password + "x"},
		{"right password, not an administrator", plain, password},
		{"unknown account", "cwnsb-it-nobody", password},
		{"password with a trailing newline", admin, password + "\n"},
	} {
		start := time.Now()
		_, err := ops.verifyMachineAccount(ctx, MachineAccountArgs{User: tc.user, Password: tc.password})
		if err == nil || err.Code != CodeMachineAccountDenied {
			t.Errorf("%s: %v, want %s", tc.name, err, CodeMachineAccountDenied)
		}
		if took := time.Since(start); took < machineCheckFloor || took > machineCheckFloor+500*time.Millisecond {
			t.Errorf("%s took %v: every refusal must take about %v", tc.name, took, machineCheckFloor)
		}
	}

	run("", "passwd", "-l", admin)
	if _, err := ops.verifyMachineAccount(ctx, MachineAccountArgs{User: admin, Password: password}); err == nil {
		t.Error("locked account accepted")
	}
}
