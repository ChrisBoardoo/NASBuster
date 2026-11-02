// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build integration && linux

// Real-kernel tests of share deletion and pool destruction, in the cwnsb-lab
// distro only (see pool_integration_test.go for how to run them).
package helper

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/crosswax/nasbuster/internal/shares"
	"github.com/crosswax/nasbuster/internal/storage"
)

func TestLabShareDeleteAndPoolDestroy(t *testing.T) {
	ctx := context.Background()
	l := newLabPool(t, 7)
	ops := l.ops
	// Snapshot names have a one-second resolution.
	ops.Now = func() time.Time { return time.Now().UTC() }

	name, res, err := l.create(storage.LayoutMirror, 0, 1)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	share := "itshare"
	if _, err := ops.shareCreate(ctx, ShareCreateArgs{Pool: name, Name: share}); err != nil {
		t.Fatalf("share create: %v", err)
	}
	dir := shares.SharePath(name, share)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := ops.snapshotCreate(ctx, SnapshotArgs{Pool: name, Share: share}); err != nil {
			t.Fatalf("snapshot: %v", err)
		}
		time.Sleep(1100 * time.Millisecond)
	}

	// The pool holds a share: never destroyed.
	if _, err := ops.poolDestroy(ctx, PoolConfirmArgs{Name: name, Confirm: name}); errCode(err) != CodePoolNotEmpty {
		t.Fatalf("destroy with a share: %v", err)
	}

	// Share deletion: read-only snapshots, subvolume, group.
	if _, err := ops.shareDelete(ctx, ShareCreateArgs{Pool: name, Name: share}); err != nil {
		t.Fatalf("share delete: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("share directory still there: %v", err)
	}
	if ops.groupExists(ctx, shares.ShareGroup(share)) {
		t.Fatal("share group still there")
	}

	// Destroy the empty mirror: kernel member list, parent disks, erase.
	out, err := ops.poolDestroy(ctx, PoolConfirmArgs{Name: name, Confirm: name})
	if err != nil || !out.Destroyed || len(out.Disks) != 2 {
		t.Fatalf("destroy mirror = %+v %v", out, err)
	}
	for _, i := range []int{0, 1} {
		if r := l.role(i); r != storage.RoleAvailable {
			t.Fatalf("disk %d after destroy = %s", i, r)
		}
	}
	if _, err := os.Stat(filepath.Join(ops.UnitDir, storage.MountUnitName(name))); !os.IsNotExist(err) {
		t.Fatal("mount unit left behind")
	}
	if _, err := os.Stat(res.MountPoint); !os.IsNotExist(err) {
		t.Fatal("mount point left behind")
	}

	// Parity: the md array is stopped and its members erased.
	pname, _, err := l.create(storage.LayoutParity1, 2, 3, 4)
	if err != nil {
		t.Fatalf("create parity: %v", err)
	}
	if out, err := ops.poolDestroy(ctx, PoolConfirmArgs{Name: pname, Confirm: pname}); err != nil || len(out.Disks) != 3 {
		t.Fatalf("destroy parity = %+v %v", out, err)
	}
	if b, _ := os.ReadFile("/proc/mdstat"); strings.Contains(string(b), strings.TrimPrefix(l.loops[2], "/dev/")) {
		t.Fatalf("array still running: %s", b)
	}
	if b, _ := os.ReadFile(ops.MDConf); strings.Contains(string(b), "pool "+pname+"\n") {
		t.Fatal("mdadm.conf block left behind")
	}
	for _, i := range []int{2, 3, 4} {
		if r := l.role(i); r != storage.RoleAvailable {
			t.Fatalf("parity disk %d after destroy = %s", i, r)
		}
	}

	// Disks gone for good: the pool is forgotten, nothing else touched.
	fname, _, err := l.create(storage.LayoutMirror, 5, 6)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	l.cmd("systemctl", "stop", storage.MountUnitName(fname))
	if _, err := ops.poolDestroy(ctx, PoolConfirmArgs{Name: fname, Confirm: fname}); errCode(err) != CodePoolNotMounted {
		t.Fatalf("destroy unmounted with disks present: %v", err)
	}
	l.unplug(5)
	l.unplug(6)
	l.cmd("btrfs", "device", "scan", "--forget")
	if out, err := ops.poolDestroy(ctx, PoolConfirmArgs{Name: fname, Confirm: fname}); err != nil || !out.Forgotten {
		t.Fatalf("forget = %+v %v", out, err)
	}
}

// A process in another mount namespace holding the pool (a service sandbox
// without shared propagation, a container with a bind mount): destroying
// the pool must refuse cleanly, name the holder, and leave the pool mounted.
func TestLabPoolDestroyHeldElsewhere(t *testing.T) {
	ctx := context.Background()
	l := newLabPool(t, 2)
	name, res, err := l.create(storage.LayoutMirror, 0, 1)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	holder := exec.Command("unshare", "--mount", "--propagation", "private", "sleep", "300")
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { holder.Process.Kill(); holder.Wait() })
	time.Sleep(300 * time.Millisecond)

	_, derr := l.ops.poolDestroy(ctx, PoolConfirmArgs{Name: name, Confirm: name})
	if errCode(derr) != CodePoolInUse || len(derr.Params["holders"].([]string)) == 0 {
		t.Fatalf("destroy while held = %v %+v", derr, derr)
	}
	if !l.ops.mountInfo(ctx, res.MountPoint).Mounted {
		t.Fatal("the pool was not put back after the refusal")
	}
	holder.Process.Kill()
	holder.Wait()
	if out, err := l.ops.poolDestroy(ctx, PoolConfirmArgs{Name: name, Confirm: name}); err != nil || !out.Destroyed {
		t.Fatalf("destroy once released = %+v %v", out, err)
	}
}
