// SPDX-License-Identifier: AGPL-3.0-or-later

package helper

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/crosswax/nasbuster/internal/shares"
	"github.com/crosswax/nasbuster/internal/sysexec"
)

// shareEnv: pool "tank" mounted; system users root/www-data exist; NAS user alice.
type shareEnv struct {
	ops          *Ops
	run          *sysexec.Fake
	files        *MemFiles
	groups       map[string]bool
	users        map[string][]string // name -> groups
	failSMB      bool
	failChown    bool
	failTestparm bool
}

func newShareEnv(t *testing.T) *shareEnv {
	t.Helper()
	e := &shareEnv{
		run: sysexec.NewFake(), files: NewMemFiles(),
		groups: map[string]bool{"root": true, "www-data": true},
		users:  map[string][]string{"root": {"root"}, "www-data": {"www-data"}, "alice": {"alice", shares.UsersGroup}},
	}
	e.groups[shares.UsersGroup] = true
	e.files.WriteFile("/etc/samba/smb.conf", []byte("# Debian default\n[global]\n   map to guest = Bad User\n"), 0)
	e.files.MkdirAll("/srv/cwnsb/tank/photos", 0)
	e.files.WriteFile("/srv/cwnsb/tank/photos/IMG_0001.jpg", []byte("x"), 0)
	e.ops = &Ops{Run: e.run, Files: e.files, SMBConf: "/etc/samba/smb.conf", Logger: quietLogger()}
	done := sysexec.Result{}
	e.run.Handle("id", func(args []string) (sysexec.Result, error) {
		g, ok := e.users[args[1]]
		if !ok {
			return sysexec.Result{ExitCode: 1}, nil
		}
		return sysexec.Result{Stdout: []byte(strings.Join(g, " ") + "\n")}, nil
	})
	e.run.Handle("getent", func(args []string) (sysexec.Result, error) {
		if e.groups[args[1]] {
			return done, nil
		}
		return sysexec.Result{ExitCode: 2}, nil
	})
	e.run.Handle("groupadd", func(args []string) (sysexec.Result, error) { e.groups[args[len(args)-1]] = true; return done, nil })
	e.run.Handle("useradd", func(args []string) (sysexec.Result, error) {
		e.users[args[len(args)-1]] = []string{args[len(args)-1], shares.UsersGroup}
		return done, nil
	})
	e.run.Handle("userdel", func(args []string) (sysexec.Result, error) { delete(e.users, args[0]); return done, nil })
	e.run.Handle("smbpasswd", func([]string) (sysexec.Result, error) {
		if e.failSMB {
			return sysexec.Result{ExitCode: 1, Stderr: []byte("Failed to add entry")}, nil
		}
		return done, nil
	})
	e.run.Handle("btrfs", func(args []string) (sysexec.Result, error) {
		if args[0] == "subvolume" && args[1] == "create" {
			e.files.MkdirAll(args[2], 0)
		}
		return done, nil
	})
	e.run.Handle("chown", func([]string) (sysexec.Result, error) {
		if e.failChown {
			return sysexec.Result{ExitCode: 1}, nil
		}
		return done, nil
	})
	e.run.Handle("chmod", func([]string) (sysexec.Result, error) { return done, nil })
	e.run.Handle("testparm", func([]string) (sysexec.Result, error) {
		if e.failTestparm {
			return sysexec.Result{ExitCode: 1, Stderr: []byte("Unknown parameter")}, nil
		}
		return done, nil
	})
	e.run.Handle("systemctl", func([]string) (sysexec.Result, error) { return done, nil })
	e.run.Handle("findmnt", func(args []string) (sysexec.Result, error) {
		if args[len(args)-1] != "/srv/cwnsb/tank" {
			return sysexec.Result{ExitCode: 1}, nil
		}
		return sysexec.Result{Stdout: []byte(`{"filesystems":[{"target":"/srv/cwnsb/tank","source":"/dev/sda1","fstype":"btrfs","uuid":"u"}]}`)}, nil
	})
	return e
}

func (e *shareEnv) ran(name string) [][]string {
	var out [][]string
	for _, c := range e.run.Calls() {
		if c[0] == name {
			out = append(out, c[1:])
		}
	}
	return out
}

func TestUserCreatePassesPasswordOnStdinOnly(t *testing.T) {
	e := newShareEnv(t)
	if _, err := e.ops.userCreate(context.Background(), UserArgs{Name: "bob", Password: "correct horse"}); err != nil {
		t.Fatal(err)
	}
	add := e.ran("useradd")
	if len(add) != 1 || !slices.Contains(add[0], "/usr/sbin/nologin") || !slices.Contains(add[0], shares.UsersGroup) {
		t.Fatalf("useradd = %v", add)
	}
	for i, c := range e.run.Calls() {
		if slices.ContainsFunc(c, func(a string) bool { return strings.Contains(a, "correct horse") }) {
			t.Fatalf("password visible in arguments: %v", c)
		}
		if c[0] == "smbpasswd" && string(e.run.Input(i)) != "correct horse\ncorrect horse\n" {
			t.Fatalf("smbpasswd stdin = %q", e.run.Input(i))
		}
	}
}

func TestUserCreateNeverAdoptsSystemAccounts(t *testing.T) {
	e := newShareEnv(t)
	// www-data passes the name rules of most tools but must never become a NAS user.
	e.users["webadmin"] = []string{"webadmin"}
	_, err := e.ops.userCreate(context.Background(), UserArgs{Name: "webadmin", Password: "correct horse"})
	if err == nil || err.Code != CodeUserNameTaken || len(e.ran("useradd")) != 0 {
		t.Fatalf("err = %v, useradd = %v", err, e.ran("useradd"))
	}
	if _, err := e.ops.userCreate(context.Background(), UserArgs{Name: "alice", Password: "correct horse"}); err == nil || err.Code != CodeUserExists {
		t.Fatalf("existing NAS user: %v", err)
	}
	for _, name := range []string{"root", "www-data", "Bad Name"} {
		if _, err := e.ops.userCreate(context.Background(), UserArgs{Name: name, Password: "correct horse"}); err == nil || err.Code != CodeUserName {
			t.Errorf("%q: %v", name, err)
		}
	}
	if _, err := e.ops.userCreate(context.Background(), UserArgs{Name: "carol", Password: "a\nb-long-enough"}); err == nil || err.Code != CodeUserPassword {
		t.Errorf("multi-line password: %v", err)
	}
}

func TestUserCreateRollsBackWhenSambaFails(t *testing.T) {
	e := newShareEnv(t)
	e.failSMB = true
	if _, err := e.ops.userCreate(context.Background(), UserArgs{Name: "bob", Password: "correct horse"}); err == nil {
		t.Fatal("expected failure")
	}
	if del := e.ran("userdel"); len(del) != 1 || del[0][0] != "bob" {
		t.Fatalf("userdel = %v", del)
	}
}

func TestUserDeleteOnlyTouchesNASUsers(t *testing.T) {
	e := newShareEnv(t)
	e.users["webadmin"] = []string{"webadmin"}
	if _, err := e.ops.userDelete(context.Background(), UserArgs{Name: "webadmin"}); err == nil || err.Code != CodeUserNotFound {
		t.Fatalf("non-NAS user: %v", err)
	}
	if len(e.ran("userdel")) != 0 {
		t.Fatal("userdel ran on a non-NAS account")
	}
	if _, err := e.ops.userDelete(context.Background(), UserArgs{Name: "alice"}); err != nil {
		t.Fatal(err)
	}
}

func TestShareCreate(t *testing.T) {
	e := newShareEnv(t)
	if _, err := e.ops.shareCreate(context.Background(), ShareCreateArgs{Pool: "data", Name: "films"}); err == nil || err.Code != CodePoolNotMounted {
		t.Fatalf("unmounted pool: %v", err)
	}
	if _, err := e.ops.shareCreate(context.Background(), ShareCreateArgs{Pool: "tank", Name: "photos"}); err == nil || err.Code != CodeShareExists {
		t.Fatalf("existing share: %v", err)
	}
	// An EMPTY subvolume left by an attempt that Samba refused is adopted;
	// one that is not a subvolume is refused like a share with files.
	e.files.MkdirAll("/srv/cwnsb/tank/retry", 0)
	if _, err := e.ops.shareCreate(context.Background(), ShareCreateArgs{Pool: "tank", Name: "retry"}); err != nil {
		t.Fatalf("empty subvolume not adopted: %v", err)
	}
	for _, c := range e.ran("btrfs") {
		if c[1] == "create" {
			t.Fatalf("adopting must not create the subvolume again: %v", c)
		}
	}
	if !slices.Equal(e.ran("chmod")[0], []string{"2770", "/srv/cwnsb/tank/retry"}) {
		t.Fatalf("adopted share without its mode: %v", e.ran("chmod"))
	}
	e.files.MkdirAll("/srv/cwnsb/tank/plain", 0)
	e.run.Handle("btrfs", func(args []string) (sysexec.Result, error) {
		if args[1] == "show" {
			return sysexec.Result{ExitCode: 1, Stderr: []byte("not a subvolume")}, nil
		}
		e.files.MkdirAll(args[2], 0)
		return sysexec.Result{}, nil
	})
	if _, err := e.ops.shareCreate(context.Background(), ShareCreateArgs{Pool: "tank", Name: "plain"}); err == nil || err.Code != CodeShareExists {
		t.Fatalf("plain directory adopted: %v", err)
	}
	res, err := e.ops.shareCreate(context.Background(), ShareCreateArgs{Pool: "tank", Name: "films"})
	if err != nil || res["path"] != "/srv/cwnsb/tank/films" {
		t.Fatalf("create = %v %v", res, err)
	}
	if chmods := e.ran("chmod"); !e.groups["cwnsb-share-films"] || !slices.Equal(chmods[len(chmods)-1], []string{"2770", "/srv/cwnsb/tank/films"}) {
		t.Fatalf("group/mode not set: %v %v", e.groups, e.ran("chmod"))
	}
}

func TestShareCreateRollsBack(t *testing.T) {
	e := newShareEnv(t)
	e.failChown = true
	if _, err := e.ops.shareCreate(context.Background(), ShareCreateArgs{Pool: "tank", Name: "films"}); err == nil {
		t.Fatal("expected failure")
	}
	if !slices.ContainsFunc(e.ran("btrfs"), func(a []string) bool { return a[1] == "delete" && a[2] == "/srv/cwnsb/tank/films" }) {
		t.Fatalf("subvolume not removed: %v", e.ran("btrfs"))
	}
}

func TestSMBApply(t *testing.T) {
	e := newShareEnv(t)
	desired := []shares.Share{{Name: "photos", Pool: "tank", Access: []shares.Access{{User: "alice", Mode: shares.ReadWrite}}}}
	if _, err := e.ops.smbApply(context.Background(), SMBApplyArgs{Shares: desired}); err != nil {
		t.Fatal(err)
	}
	conf, _ := e.files.ReadFile("/etc/samba/smb.conf")
	if !strings.HasPrefix(string(conf), shares.Header) || !strings.Contains(string(conf), "write list = alice") {
		t.Fatalf("smb.conf = %s", conf)
	}
	orig, _ := e.files.ReadFile("/etc/samba/smb.conf.cwnsb-orig")
	if !strings.Contains(string(orig), "Debian default") {
		t.Fatalf("original not backed up: %q", orig)
	}
	if tp := e.ran("testparm"); len(tp) != 1 || tp[0][1] != "/etc/samba/smb.conf.cwnsb-new" {
		t.Fatalf("testparm = %v", tp)
	}
	if e.files.Exists("/etc/samba/smb.conf.cwnsb-new") {
		t.Fatal("temporary file left behind")
	}

	// Second apply: the original backup is never overwritten by our own file.
	if _, err := e.ops.smbApply(context.Background(), SMBApplyArgs{Shares: desired}); err != nil {
		t.Fatal(err)
	}
	if orig2, _ := e.files.ReadFile("/etc/samba/smb.conf.cwnsb-orig"); string(orig2) != string(orig) {
		t.Fatal("original backup overwritten")
	}
}

func TestSMBApplyRefuses(t *testing.T) {
	cases := map[string][]shares.Share{
		"user not a NAS user": {{Name: "photos", Pool: "tank", Access: []shares.Access{{User: "webadmin", Mode: shares.ReadWrite}}}},
		"missing dir":         {{Name: "films", Pool: "tank"}},
		"pool unmounted":      {{Name: "photos", Pool: "data"}},
		"injection":           {{Name: "x\n[global]", Pool: "tank"}},
	}
	for name, desired := range cases {
		e := newShareEnv(t)
		e.users["webadmin"] = []string{"webadmin"}
		before, _ := e.files.ReadFile("/etc/samba/smb.conf")
		if _, err := e.ops.smbApply(context.Background(), SMBApplyArgs{Shares: desired}); err == nil {
			t.Errorf("%s: accepted", name)
		}
		after, _ := e.files.ReadFile("/etc/samba/smb.conf")
		if string(before) != string(after) || len(e.ran("systemctl")) != 0 {
			t.Errorf("%s: smb.conf changed or Samba reloaded after a refusal", name)
		}
	}
}

func TestSMBApplyKeepsConfigWhenTestparmFails(t *testing.T) {
	e := newShareEnv(t)
	e.failTestparm = true
	before, _ := e.files.ReadFile("/etc/samba/smb.conf")
	if _, err := e.ops.smbApply(context.Background(), SMBApplyArgs{Shares: []shares.Share{{Name: "photos", Pool: "tank"}}}); err == nil {
		t.Fatal("expected failure")
	}
	after, _ := e.files.ReadFile("/etc/samba/smb.conf")
	if string(before) != string(after) || len(e.ran("systemctl")) != 0 {
		t.Fatal("a configuration rejected by testparm was installed")
	}
}

func TestSMBSessions(t *testing.T) {
	e := newShareEnv(t)
	b, err := os.ReadFile("../shares/testdata/smbstatus.json")
	if err != nil {
		t.Fatal(err)
	}
	e.run.Set(sysexec.Result{Stdout: b}, "smbstatus", "--json")
	res, opErr := e.ops.smbSessions(context.Background())
	if opErr != nil || len(res.Sessions) != 1 || res.Sessions[0].User != "probeuser" || len(res.Connections) != 1 {
		t.Fatalf("sessions = %+v %v", res, opErr)
	}
	e.run.Set(sysexec.Result{ExitCode: 1}, "smbstatus", "--json") // Samba stopped
	if res, opErr := e.ops.smbSessions(context.Background()); opErr != nil || len(res.Sessions) != 0 {
		t.Fatalf("stopped = %+v %v", res, opErr)
	}
}

func TestSnapshots(t *testing.T) {
	e := newShareEnv(t)
	clock := time.Date(2026, 10, 1, 21, 0, 0, 0, time.UTC)
	e.ops.Now = func() time.Time { return clock }
	e.run.Handle("btrfs", func(args []string) (sysexec.Result, error) {
		switch {
		case args[0] == "subvolume" && args[1] == "snapshot":
			e.files.MkdirAll(args[4], 0)
		case args[0] == "subvolume" && args[1] == "delete":
			e.files.Remove(args[2])
		}
		return sysexec.Result{}, nil
	})
	ctx := context.Background()
	args := SnapshotArgs{Pool: "tank", Share: "photos"}

	if res, err := e.ops.snapshotList(ctx, args); err != nil || len(res.Snapshots) != 0 {
		t.Fatalf("empty list = %+v %v", res, err)
	}
	first, err := e.ops.snapshotCreate(ctx, args)
	if err != nil || first["name"] != "@GMT-2026.10.01-21.00.00" {
		t.Fatalf("create = %v %v", first, err)
	}
	snap := e.ran("btrfs")[0]
	if !slices.Equal(snap, []string{"subvolume", "snapshot", "-r", "/srv/cwnsb/tank/photos", "/srv/cwnsb/tank/photos/.snapshots/@GMT-2026.10.01-21.00.00"}) {
		t.Fatalf("btrfs = %v (must be read-only, inside .snapshots)", snap)
	}
	if ch := e.ran("chmod"); len(ch) != 1 || ch[0][0] != "0750" {
		t.Fatalf(".snapshots mode = %v", ch)
	}
	if _, err := e.ops.snapshotCreate(ctx, args); err == nil || err.Code != CodeSnapshotExist {
		t.Fatalf("same second: %v", err)
	}
	clock = clock.Add(time.Hour)
	e.ops.snapshotCreate(ctx, args)
	e.files.MkdirAll("/srv/cwnsb/tank/photos/.snapshots/not-a-snapshot", 0)
	res, _ := e.ops.snapshotList(ctx, args)
	if !slices.Equal(res.Snapshots, []string{"@GMT-2026.10.01-22.00.00", "@GMT-2026.10.01-21.00.00"}) {
		t.Fatalf("list = %v (newest first, foreign entries ignored)", res.Snapshots)
	}

	for _, bad := range []string{"../photos", "not-a-snapshot", "@GMT-2026.10.01-21.00.00/../..", ""} {
		if _, err := e.ops.snapshotDelete(ctx, SnapshotArgs{Pool: "tank", Share: "photos", Name: bad}); err == nil || err.Code != CodeSnapshotName {
			t.Errorf("delete %q: %v", bad, err)
		}
	}
	if _, err := e.ops.snapshotDelete(ctx, SnapshotArgs{Pool: "tank", Share: "photos", Name: "@GMT-2020.01.01-00.00.00"}); err == nil || err.Code != CodeSnapshotNone {
		t.Fatalf("missing snapshot: %v", err)
	}
	if _, err := e.ops.snapshotDelete(ctx, SnapshotArgs{Pool: "tank", Share: "photos", Name: "@GMT-2026.10.01-21.00.00"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ops.snapshotCreate(ctx, SnapshotArgs{Pool: "data", Share: "photos"}); err == nil || err.Code != CodePoolNotMounted {
		t.Fatalf("unmounted pool: %v", err)
	}
}

func TestSMBApplyStopsNetBIOSOnTakeover(t *testing.T) {
	e := newShareEnv(t)
	nmbdRestarts := func() int {
		n := 0
		for _, c := range e.ran("systemctl") {
			if slices.Equal(c, []string{"try-restart", "nmbd"}) {
				n++
			}
		}
		return n
	}
	for i := range 2 {
		if _, err := e.ops.smbApply(context.Background(), SMBApplyArgs{Shares: []shares.Share{{Name: "photos", Pool: "tank"}}}); err != nil {
			t.Fatal(err)
		}
		// Only when we replace a foreign smb.conf (seen on the Pi 5: nmbd
		// started by the samba package kept running with NetBIOS disabled).
		if got := nmbdRestarts(); got != 1 {
			t.Fatalf("apply %d: %d nmbd try-restarts, want 1", i, got)
		}
	}
}

func TestSMBApplyRestartsWhenTimeMachineChanges(t *testing.T) {
	e := newShareEnv(t)
	apply := func(tm bool) string {
		t.Helper()
		if _, err := e.ops.smbApply(context.Background(), SMBApplyArgs{Shares: []shares.Share{{Name: "photos", Pool: "tank", TimeMachine: tm}}}); err != nil {
			t.Fatal(err)
		}
		calls := e.ran("systemctl")
		return calls[len(calls)-1][0]
	}
	// mDNS records (_adisk._tcp) are only published when smbd starts.
	for i, step := range []struct {
		tm   bool
		want string
	}{{false, "reload-or-restart"}, {true, "restart"}, {true, "reload-or-restart"}, {false, "restart"}} {
		if got := apply(step.tm); got != step.want {
			t.Fatalf("step %d (timeMachine=%v): %s, want %s", i, step.tm, got, step.want)
		}
	}
}
