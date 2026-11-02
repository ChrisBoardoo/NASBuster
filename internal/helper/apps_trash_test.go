// SPDX-License-Identifier: AGPL-3.0-or-later

package helper

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/crosswax/nasbuster/internal/sysexec"
)

// newTrashOps: tank is mounted, Docker is not installed (its containers are
// not this test's concern), and btrfs answers as told.
func newTrashOps(files *MemFiles, now time.Time, failSnapshot bool) (*Ops, *sysexec.Fake) {
	run := sysexec.NewFake()
	run.Handle("findmnt", func(args []string) (sysexec.Result, error) {
		if args[len(args)-1] != "/srv/cwnsb/tank" {
			return sysexec.Result{ExitCode: 1}, nil
		}
		return sysexec.Result{Stdout: []byte(`{"filesystems":[{"target":"/srv/cwnsb/tank","source":"/dev/sda1","fstype":"btrfs","uuid":"u"}]}`)}, nil
	})
	run.Handle("systemctl", func([]string) (sysexec.Result, error) { return sysexec.Result{}, nil })
	run.Handle("dpkg-query", func([]string) (sysexec.Result, error) { return sysexec.Result{}, nil })
	run.Handle("btrfs", func(args []string) (sysexec.Result, error) {
		if failSnapshot && args[1] == "snapshot" {
			return sysexec.Result{ExitCode: 1, Stderr: []byte("ERROR: cannot snapshot: No space left on device")}, nil
		}
		return sysexec.Result{}, nil
	})
	return &Ops{Run: run, Files: files, Logger: quietLogger(), Now: func() time.Time { return now }}, run
}

func btrfsCalls(run *sysexec.Fake) []string {
	var out []string
	for _, c := range run.Calls() {
		if c[0] == "btrfs" {
			out = append(out, strings.Join(c[1:], " "))
		}
	}
	return out
}

// TestAppRemoveKeepsDataInTrash: "also delete its data" first snapshots the
// application's subvolume, read-only, into the pool's trash (ADR-0035).
func TestAppRemoveKeepsDataInTrash(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 30, 9, 18, 0, 0, time.UTC)
	files := NewMemFiles()
	files.WriteFile("/srv/cwnsb/tank/.apps/uptime-kuma/compose.yaml", []byte("services: {}\n"), 0o600)
	o, run := newTrashOps(files, now, false)

	if _, err := o.appRemove(ctx, AppRemoveArgs{Pool: "tank", Name: "uptime-kuma", DeleteData: true}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"subvolume show /srv/cwnsb/tank/.apps/uptime-kuma",
		"subvolume snapshot -r /srv/cwnsb/tank/.apps/uptime-kuma /srv/cwnsb/tank/.cwnsb/trash/app-uptime-kuma-20261030T091800Z",
		"subvolume delete /srv/cwnsb/tank/.apps/uptime-kuma",
	}
	if got := btrfsCalls(run); !slices.Equal(got, want) {
		t.Fatalf("btrfs calls = %q", got)
	}
	if !files.Exists("/srv/cwnsb/tank/.cwnsb/trash") {
		t.Fatal("trash folder not created")
	}

	// Keeping the data: nothing is snapshotted, nothing deleted.
	o, run = newTrashOps(files, now, false)
	if _, err := o.appRemove(ctx, AppRemoveArgs{Pool: "tank", Name: "uptime-kuma"}); err != nil {
		t.Fatal(err)
	}
	if got := btrfsCalls(run); len(got) != 0 {
		t.Fatalf("data kept, btrfs calls = %q", got)
	}
}

// TestAppRemoveRefusedWithoutSnapshot: no snapshot, no deletion.
func TestAppRemoveRefusedWithoutSnapshot(t *testing.T) {
	files := NewMemFiles()
	files.WriteFile("/srv/cwnsb/tank/.apps/portainer/compose.yaml", []byte("services: {}\n"), 0o600)
	o, run := newTrashOps(files, time.Now(), true)
	if _, err := o.appRemove(context.Background(), AppRemoveArgs{Pool: "tank", Name: "portainer", DeleteData: true}); err == nil {
		t.Fatal("removed without a snapshot")
	}
	for _, c := range btrfsCalls(run) {
		if strings.HasPrefix(c, "subvolume delete") {
			t.Fatalf("deleted anyway: %q", btrfsCalls(run))
		}
	}
}

// TestAppTrashPurge: only entries older than a week, and only ours, go.
func TestAppTrashPurge(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	files := NewMemFiles()
	for _, e := range []string{
		"app-portainer-20261030T091800Z",   // 7 days and 3 hours ago: purged
		"app-uptime-kuma-20261030T130000Z", // 6 days and 23 hours ago: kept
		"notes",                            // not ours: never touched
	} {
		files.MkdirAll("/srv/cwnsb/tank/.cwnsb/trash/"+e, 0o700)
	}
	o, run := newTrashOps(files, now, false)
	res, err := o.appTrashPurge(ctx, TrashPurgeArgs{Pool: "tank"})
	if err != nil || res["purged"] != 1 {
		t.Fatalf("purge = %v %v", res, err)
	}
	if got := btrfsCalls(run); !slices.Equal(got, []string{"subvolume delete /srv/cwnsb/tank/.cwnsb/trash/app-portainer-20261030T091800Z"}) {
		t.Fatalf("btrfs calls = %q", got)
	}

	// A pool without a trash, or a bad name.
	if res, err := o.appTrashPurge(ctx, TrashPurgeArgs{Pool: "e2"}); err != nil || res["purged"] != 0 {
		t.Fatalf("no trash = %v %v", res, err)
	}
	if _, err := o.appTrashPurge(ctx, TrashPurgeArgs{Pool: "../x"}); err == nil {
		t.Fatal("bad pool name accepted")
	}
}
