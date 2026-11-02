// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build integration && linux

// Real SMB tests in the cwnsb-lab distro: real accounts, real Samba, real
// smbclient connections over SMB3 with passwords, on a real Btrfs pool.
package helper

import (
	"context"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/crosswax/nasbuster/internal/shares"
	"github.com/crosswax/nasbuster/internal/storage"
)

func smbclient(t *testing.T, share, userName, password string, commands string) (string, int) {
	t.Helper()
	cmd := exec.Command("/usr/bin/smbclient", "//127.0.0.1/"+share, "-U", userName+"%"+password, "-m", "SMB3", "-c", commands)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		code = 1
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
	}
	return string(out), code
}

func TestLabSharesOverSMB(t *testing.T) {
	l := newLabPool(t, 2)
	if _, err := os.Stat("/usr/bin/smbclient"); err != nil {
		t.Skip("samba/smbclient not installed in the lab (see tools/lab/provision.sh)")
	}
	ctx := context.Background()
	pool, _, opErr := l.create(storage.LayoutMirror, 0, 1)
	if opErr != nil {
		t.Fatalf("pool: %v", opErr)
	}
	suffix := rand.IntN(100000)
	alice, bob := fmt.Sprintf("ita%d", suffix), fmt.Sprintf("itb%d", suffix)
	share := fmt.Sprintf("itshare%d", suffix)
	const pwA, pwB = "alice long password", "bob long password"
	ops := l.ops
	t.Cleanup(func() {
		if list, err := ops.snapshotList(ctx, SnapshotArgs{Pool: pool, Share: share}); err == nil {
			for _, n := range list.Snapshots {
				ops.snapshotDelete(ctx, SnapshotArgs{Pool: pool, Share: share, Name: n})
			}
		}
		ops.smbApply(ctx, SMBApplyArgs{Shares: nil})
		for _, u := range []string{alice, bob} {
			ops.userDelete(ctx, UserArgs{Name: u})
		}
		l.run.Run(ctx, "btrfs", "subvolume", "delete", shares.SharePath(pool, share))
		l.run.Run(ctx, "groupdel", shares.ShareGroup(share))
	})

	for u, pw := range map[string]string{alice: pwA, bob: pwB} {
		if _, err := ops.userCreate(ctx, UserArgs{Name: u, Password: pw}); err != nil {
			t.Fatalf("create %s: %v", u, err)
		}
	}
	if _, err := ops.userCreate(ctx, UserArgs{Name: "games", Password: pwA}); err == nil {
		t.Fatal("a system account name was accepted")
	}
	if _, err := ops.shareCreate(ctx, ShareCreateArgs{Pool: pool, Name: share}); err != nil {
		t.Fatalf("share: %v", err)
	}
	desired := []shares.Share{{Name: share, Pool: pool, Access: []shares.Access{
		{User: alice, Mode: shares.ReadWrite}, {User: bob, Mode: shares.ReadOnly}}}}
	if _, err := ops.smbApply(ctx, SMBApplyArgs{Shares: desired}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	time.Sleep(time.Second) // smbd restarting

	// alice (rw) writes; bob (ro) reads but cannot write; a wrong password fails.
	tmp := filepath.Join(t.TempDir(), "payload.txt")
	os.WriteFile(tmp, []byte("CrossWax NasBuster over SMB3"), 0o644)
	if out, code := smbclient(t, share, alice, pwA, "put "+tmp+" hello.txt"); code != 0 {
		t.Fatalf("alice put: %d %s", code, out)
	}
	if out, code := smbclient(t, share, bob, pwB, "get hello.txt "+filepath.Join(t.TempDir(), "got.txt")); code != 0 {
		t.Fatalf("bob get: %d %s", code, out)
	}
	if out, code := smbclient(t, share, bob, pwB, "put "+tmp+" bob.txt"); code == 0 || !strings.Contains(out, "ACCESS_DENIED") {
		t.Fatalf("bob (read-only) could write: %d %s", code, out)
	}
	if out, code := smbclient(t, share, alice, "wrong password", "ls"); code == 0 {
		t.Fatalf("wrong password accepted: %s", out)
	}

	// On disk: the file belongs to the share's group, readable by the group.
	info, err := os.Stat(filepath.Join(shares.SharePath(pool, share), "hello.txt"))
	if err != nil {
		t.Fatal(err)
	}
	grp, _ := user.LookupGroup(shares.ShareGroup(share))
	if mode := info.Mode().Perm(); mode != 0o660 {
		t.Errorf("file mode = %o, want 660", mode)
	}
	if st := statGID(info); grp == nil || fmt.Sprint(st) != grp.Gid {
		t.Errorf("file gid = %v, want %s (%+v)", st, shares.ShareGroup(share), grp)
	}

	// A live session shows up in smb.sessions (the onboarding's "connected!").
	cmd := exec.Command("/usr/bin/smbclient", "//127.0.0.1/"+share, "-U", alice+"%"+pwA, "-m", "SMB3")
	stdin, _ := cmd.StdinPipe()
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	io.WriteString(stdin, "ls\n")
	time.Sleep(1500 * time.Millisecond)
	res, sErr := ops.smbSessions(ctx)
	stdin.Close()
	cmd.Wait()
	if sErr != nil || !sessionFor(res, alice, share) {
		t.Fatalf("sessions = %+v %v", res, sErr)
	}

	// Windows "Previous Versions": snapshot, overwrite, read the old version back.
	snap, sErr2 := ops.snapshotCreate(ctx, SnapshotArgs{Pool: pool, Share: share})
	if sErr2 != nil {
		t.Fatalf("snapshot: %v", sErr2)
	}
	os.WriteFile(tmp, []byte("overwritten by mistake"), 0o644)
	if out, code := smbclient(t, share, alice, pwA, "put "+tmp+" hello.txt"); code != 0 {
		t.Fatalf("overwrite: %d %s", code, out)
	}
	if out, _ := smbclient(t, share, bob, pwB, "allinfo hello.txt"); !strings.Contains(out, snap["name"]) {
		t.Fatalf("allinfo does not list %s: %s", snap["name"], out)
	}
	old := filepath.Join(t.TempDir(), "old.txt")
	if out, code := smbclient(t, share, bob, pwB, "get "+snap["name"]+"/hello.txt "+old); code != 0 {
		t.Fatalf("get previous version: %d %s", code, out)
	}
	if b, _ := os.ReadFile(old); string(b) != "CrossWax NasBuster over SMB3" {
		t.Fatalf("previous version = %q", b)
	}
	// Snapshots are read-only, even for a read-write user.
	if out, code := smbclient(t, share, alice, pwA, "put "+tmp+" "+snap["name"]+"/hack.txt"); code == 0 {
		t.Fatalf("wrote into a snapshot: %s", out)
	}
}

func sessionFor(res SMBSessionsResult, userName, share string) bool {
	userOK, shareOK := false, false
	for _, s := range res.Sessions {
		userOK = userOK || s.User == userName
	}
	for _, c := range res.Connections {
		shareOK = shareOK || c.Share == share
	}
	return userOK && shareOK
}
