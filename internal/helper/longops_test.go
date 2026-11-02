// SPDX-License-Identifier: AGPL-3.0-or-later

package helper

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/crosswax/nasbuster/internal/sysexec"
)

// fakeUnits answers systemd-run, and systemctl/journalctl about the unit it
// started: unknown before, then in state ("activating", "failed"...).
func fakeUnits(run *sysexec.Fake, state string, journal ...string) {
	started := false
	run.Handle("systemd-run", func([]string) (sysexec.Result, error) {
		started = true
		return sysexec.Result{}, nil
	})
	run.Handle("systemctl", func(args []string) (sysexec.Result, error) {
		if args[0] != "show" {
			return sysexec.Result{}, nil
		}
		if !started {
			return sysexec.Result{Stdout: []byte("LoadState=not-found\nActiveState=inactive\n")}, nil
		}
		result := "success"
		if state == "failed" {
			result = "exit-code"
		}
		return sysexec.Result{Stdout: []byte("LoadState=loaded\nActiveState=" + state + "\nResult=" + result + "\nExecMainStatus=1\n")}, nil
	})
	run.Handle("journalctl", func([]string) (sysexec.Result, error) {
		return sysexec.Result{Stdout: []byte(strings.Join(journal, "\n") + "\n")}, nil
	})
}

func systemdRunArgs(t *testing.T, run *sysexec.Fake) []string {
	t.Helper()
	for _, c := range run.Calls() {
		if c[0] == "systemd-run" {
			return c[1:]
		}
	}
	t.Fatal("systemd-run was not called")
	return nil
}

func TestScrubRunsInItsOwnUnit(t *testing.T) {
	e := newPoolEnv(t)
	mnt := "/srv/cwnsb/tank"
	e.mounted[mnt] = true
	e.run.Set(sysexec.Result{Stdout: []byte("UUID: x\nStatus: finished\n")}, "btrfs", "scrub", "status", "-R", mnt)
	fakeUnits(e.run, "activating")

	if _, err := e.ops.poolScrub(context.Background(), PoolArgs{Name: "tank"}); err != nil {
		t.Fatalf("scrub: %v", err)
	}
	args := systemdRunArgs(t, e.run)
	// Outside the helper's cgroup, in the foreground, without a time limit.
	if !slices.Contains(args, "--unit=cwnsb-job-scrub-tank") || !slices.Contains(args, "TimeoutStartSec=infinity") {
		t.Errorf("unit options = %v", args)
	}
	if got := strings.Join(args[len(args)-5:], " "); got != "/usr/bin/btrfs scrub start -B "+mnt {
		t.Errorf("command = %q", got)
	}
}

func TestScrubRefusedWhileRunning(t *testing.T) {
	e := newPoolEnv(t)
	mnt := "/srv/cwnsb/tank"
	e.mounted[mnt] = true
	e.run.Set(sysexec.Result{Stdout: []byte("UUID: x\nStatus: running\n")}, "btrfs", "scrub", "status", "-R", mnt)
	fakeUnits(e.run, "activating")

	if _, err := e.ops.poolScrub(context.Background(), PoolArgs{Name: "tank"}); err == nil || err.Code != CodePoolBusy {
		t.Fatalf("scrub while one runs: %v", err)
	}
	if slices.ContainsFunc(e.run.Calls(), func(c []string) bool { return c[0] == "systemd-run" }) {
		t.Error("a second scrub was started")
	}
}

func TestPoolJobEarlyFailureNamesTheError(t *testing.T) {
	e := newPoolEnv(t)
	fakeUnits(e.run, "failed",
		"Started cwnsb-job-balance-tank.service.",
		"ERROR: unable to start balance, another exclusive operation 'device replace' in progress",
		"cwnsb-job-balance-tank.service: Main process exited, code=exited, status=1/FAILURE")

	err := e.ops.startPoolJob(context.Background(), "balance-tank", nil, []string{btrfsBin, "balance", "start", "/srv/cwnsb/tank"})
	if err == nil || err.Code != CodeFailed || !strings.Contains(err.Message, "another exclusive operation") {
		t.Fatalf("early failure = %v", err)
	}
}

// After a replace, udev is told about the new member's file system (seen on
// the Pi 5: lsblk showed it empty). ExecStartPost runs only after a success;
// "-" keeps a udev hiccup from failing a good replace.
func TestPoolJobThenAddsExecStartPost(t *testing.T) {
	e := newPoolEnv(t)
	fakeUnits(e.run, "activating")
	post := "-/usr/bin/udevadm trigger --action=change /dev/sdd1"
	err := e.ops.startPoolJobThen(context.Background(), "replace-tank", nil, []string{post},
		[]string{btrfsBin, "replace", "start", "-B", "2", "/dev/sdd1", "/srv/cwnsb/tank"})
	if err != nil {
		t.Fatal(err)
	}
	if args := systemdRunArgs(t, e.run); !slices.Contains(args, "ExecStartPost="+post) {
		t.Fatalf("unit options = %v", args)
	}
}

func TestPoolJobsHaveNoTimeLimit(t *testing.T) {
	for job, want := range map[string]string{
		"scrub-tank": "infinity", "balance-tank": "infinity", "replace-tank": "infinity", "resync-tank": "infinity",
		"import-photos": "infinity", "engine": "45min", "app-portainer": "45min",
	} {
		if got := jobTimeout(job); got != want {
			t.Errorf("jobTimeout(%q) = %s, want %s", job, got, want)
		}
	}
}

func TestScrubResumeOnlyAStoppedOne(t *testing.T) {
	for status, wantErr := range map[string]string{"interrupted": "", "aborted": "", "finished": CodeScrubNotStopped, "running": CodePoolBusy} {
		e := newPoolEnv(t)
		mnt := "/srv/cwnsb/tank"
		e.mounted[mnt] = true
		e.run.Set(sysexec.Result{Stdout: []byte("Status: " + status + "\n")}, "btrfs", "scrub", "status", "-R", mnt)
		fakeUnits(e.run, "activating")
		_, err := e.ops.poolScrubResume(context.Background(), PoolArgs{Name: "tank"})
		switch {
		case wantErr == "" && err != nil:
			t.Errorf("%s: %v", status, err)
		case wantErr != "" && (err == nil || err.Code != wantErr):
			t.Errorf("%s: err = %v, want %s", status, err, wantErr)
		case wantErr == "":
			// Goes on in the scrub's own unit, where it stopped.
			args := systemdRunArgs(t, e.run)
			if got := strings.Join(args[len(args)-5:], " "); got != "/usr/bin/btrfs scrub resume -B "+mnt {
				t.Errorf("%s: command = %q", status, got)
			}
		}
	}
}
