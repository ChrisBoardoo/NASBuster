// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build integration && linux

// Real-system test of configuration backup and restore (ADR-0014): real
// accounts, real Samba database, in the cwnsb-lab distro only.
package helper

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/crosswax/nasbuster/internal/backup"
	"github.com/crosswax/nasbuster/internal/shares"
	"github.com/crosswax/nasbuster/internal/storage"
	"github.com/crosswax/nasbuster/internal/store"
)

func (l *labPool) getent(db, name string) string {
	res, _ := l.run.Run(context.Background(), "getent", db, name)
	return strings.TrimSpace(string(res.Stdout))
}

// freeGID returns a system GID that no group holds.
func (l *labPool) freeGID() int {
	l.t.Helper()
	for gid := 900; gid < 1000; gid++ {
		if l.getent("group", strconv.Itoa(gid)) == "" {
			return gid
		}
	}
	l.t.Fatal("no free GID between 900 and 999")
	return 0
}

func (l *labPool) sambaLine(user string) string {
	res, _ := l.run.Run(context.Background(), "pdbedit", "-L", "-w")
	for _, line := range strings.Split(string(res.Stdout), "\n") {
		if strings.HasPrefix(line, user+":") {
			f := strings.Split(line, ":")
			return f[0] + ":" + f[1] + ":" + f[3] // name, uid, NT hash (not the change time)
		}
	}
	return ""
}

func TestLabConfigBackupRestore(t *testing.T) {
	ctx := context.Background()
	l := newLabPool(t, 2)
	pool, _, err := l.create(storage.LayoutMirror, 0, 1)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	user, share := "itrestore", "itdocs"
	t.Cleanup(func() {
		l.ops.userDelete(ctx, UserArgs{Name: user})
		l.run.Run(ctx, "groupdel", shares.ShareGroup(share))
		os.RemoveAll(filepath.Dir(RestoreTarget))
	})
	if _, err := l.ops.userCreate(ctx, UserArgs{Name: user, Password: "a long lab password"}); err != nil {
		t.Fatalf("user: %v", err)
	}
	if _, err := l.ops.shareCreate(ctx, ShareCreateArgs{Pool: pool, Name: share}); err != nil {
		t.Fatalf("share: %v", err)
	}
	passwdBefore, groupBefore := l.getent("passwd", user), l.getent("group", shares.ShareGroup(share))
	sambaBefore := l.sambaLine(user)
	if passwdBefore == "" || groupBefore == "" || sambaBefore == "" {
		t.Fatalf("setup: %q %q %q", passwdBefore, groupBefore, sambaBefore)
	}

	// The daemon's export: a real SQLite database.
	src, serr := store.Open(ctx, filepath.Join(t.TempDir(), "cwnsb.db"))
	if serr != nil {
		t.Fatal(serr)
	}
	export := filepath.Join(t.TempDir(), "config.db")
	if err := src.ExportTo(ctx, export); err != nil {
		t.Fatal(err)
	}
	src.Close()
	l.ops.ReadDaemonExport = func() ([]byte, error) { return os.ReadFile(export) }

	res, berr := l.ops.configBackup(ctx, BackupArgs{Installation: "0123456789abcdef0123456789abcdef", Admin: "admin", Shares: []string{}})
	if berr != nil || len(res.Pools) == 0 {
		t.Fatalf("backup = %+v %v", res, berr)
	}
	dir := filepath.Join(backup.Dir(pool), res.Name)
	for _, f := range []string{backup.DatabaseFile, backup.SambaFile, backup.ManifestFile} {
		st, err := os.Stat(filepath.Join(dir, f))
		if err != nil || st.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v %v", f, st, err)
		}
	}
	if st, _ := os.Stat(filepath.Join(storage.MountPoint(pool), ".cwnsb")); st.Mode().Perm() != 0o700 {
		t.Fatalf(".cwnsb mode = %v", st.Mode())
	}

	// The SD card dies: the accounts and the Samba database are gone.
	if _, err := l.ops.userDelete(ctx, UserArgs{Name: user}); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	l.cmd("groupdel", shares.ShareGroup(share))
	if l.getent("passwd", user) != "" || l.sambaLine(user) != "" {
		t.Fatal("simulated loss incomplete")
	}
	// The fresh system already had a NAS user before the restore: it holds
	// cwnsb-users under another GID (Pi 5, phase F). It owns no file.
	l.cmd("groupmod", "--gid", strconv.Itoa(l.freeGID()), shares.UsersGroup)

	out, rerr := l.ops.configRestore(ctx, RestoreArgs{Pool: pool, Name: res.Name})
	if rerr != nil || len(out.Conflicts) != 0 || !out.Restart {
		t.Fatalf("restore = %+v %v", out, rerr)
	}
	usersGroup := l.getent("group", shares.UsersGroup)
	if !slices.Contains(strings.Split(usersGroup[strings.LastIndex(usersGroup, ":")+1:], ","), user) {
		t.Fatalf("%s = %q: %s must be a member again", shares.UsersGroup, usersGroup, user)
	}
	if got := l.getent("passwd", user); got != passwdBefore {
		t.Fatalf("account: %q, want %q (same UID: it owns the files)", got, passwdBefore)
	}
	if got := l.getent("group", shares.ShareGroup(share)); got != groupBefore {
		t.Fatalf("share group: %q, want %q", got, groupBefore)
	}
	if got := l.sambaLine(user); got != sambaBefore {
		t.Fatalf("Samba password: %q, want %q", got, sambaBefore)
	}
	if b, err := os.ReadFile(RestoreTarget); err != nil || !backup.IsSQLite(b) {
		t.Fatalf("database left for the daemon: %v", err)
	}

	// A second restore: everything is already right, nothing conflicts.
	if out, rerr := l.ops.configRestore(ctx, RestoreArgs{Pool: pool, Name: res.Name}); rerr != nil || len(out.Conflicts) != 0 || len(out.Users) != 1 {
		t.Fatalf("second restore = %+v %v", out, rerr)
	}
}
