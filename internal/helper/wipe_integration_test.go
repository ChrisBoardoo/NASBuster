// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build integration && linux

// Real-kernel tests of disk wiping, in the cwnsb-lab distro only (see
// pool_integration_test.go for how to run them).
package helper

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/crosswax/nasbuster/internal/storage"
)

func (l *labPool) wipe(i int) *Error {
	_, err := l.ops.diskWipe(context.Background(), DiskWipeArgs{ID: l.id(i), Confirm: l.id(i)})
	return err
}

func (l *labPool) role(i int) string {
	l.t.Helper()
	disks, err := l.ops.Discover(context.Background())
	if err != nil {
		l.t.Fatal(err)
	}
	for _, d := range disks {
		if d.ID == l.id(i) {
			return d.Role
		}
	}
	return "absent"
}

func TestLabWipe(t *testing.T) {
	l := newLabPool(t, 7)
	ctx := context.Background()

	// A pre-formatted disk (factory partition + filesystem) becomes usable.
	l.cmd("sgdisk", "-n", "1:0:0", "-t", "1:0700", l.loops[0])
	l.cmd("udevadm", "settle")
	l.cmd("mkfs.ext4", "-q", "-L", "FACTORY", l.loops[0]+"p1")
	l.cmd("udevadm", "settle")
	if r := l.role(0); r != storage.RoleInUse {
		t.Fatalf("pre-formatted role = %s", r)
	}
	if err := l.wipe(0); err != nil {
		t.Fatalf("wipe pre-formatted: %v", err)
	}
	if r := l.role(0); r != storage.RoleAvailable {
		t.Fatalf("after wipe = %s", r)
	}

	// A mounted filesystem is never wiped.
	l.cmd("mkfs.ext4", "-q", l.loops[1])
	l.cmd("udevadm", "settle")
	mnt := t.TempDir()
	l.cmd("mount", l.loops[1], mnt)
	if err := l.wipe(1); errCode(err) != CodeDiskMounted {
		t.Fatalf("mounted: %v", err)
	}
	l.cmd("umount", mnt)

	// Live members of a mounted mirror: the second one is only known
	// through /sys/fs/btrfs/<uuid>/devices (lsblk shows one mount source).
	name, res, err := l.create(storage.LayoutMirror, 2, 3)
	if err != nil {
		t.Fatalf("create mirror: %v", err)
	}
	if _, statErr := os.Stat("/sys/fs/btrfs/" + res.UUID + "/devices/" + l.loops[3][len("/dev/"):] + "p1"); statErr != nil {
		t.Fatalf("sysfs naming assumption: %v", statErr)
	}
	for _, i := range []int{2, 3} {
		if err := l.wipe(i); errCode(err) != CodeDiskInPool && errCode(err) != CodeDiskMounted {
			t.Fatalf("live member %d: %v", i, err)
		}
	}
	// Pool not mounted (waiting for a decision): its disks stay untouchable.
	l.cmd("systemctl", "stop", storage.MountUnitName(name))
	for _, i := range []int{2, 3} {
		if err := l.wipe(i); errCode(err) != CodeDiskInPool {
			t.Fatalf("member %d of an unmounted pool: %v", i, err)
		}
	}
	l.cmd("systemctl", "start", storage.MountUnitName(name))

	// A running md array holds its members.
	if _, _, err := l.create(storage.LayoutParity1, 4, 5, 6); err != nil {
		t.Fatalf("create parity: %v", err)
	}
	// Refused as held (md127), mounted (lsblk shows the md mount under each
	// member) or pool member: whichever check comes first.
	if err := l.wipe(4); errCode(err) != CodeDiskBusy && errCode(err) != CodeDiskInPool && errCode(err) != CodeDiskMounted {
		t.Fatalf("md member: %v", err)
	}
	if _, err := l.ops.diskWipe(ctx, DiskWipeArgs{ID: l.id(5), Confirm: "nope"}); errCode(err) != CodeConfirm {
		t.Fatalf("confirm: %v", err)
	}
}

// Another tool's quick wipe (OMV, sgdisk --zap-all) removes the partition
// table only: the old file system stays 1 MiB in, exactly where the pool's
// partition starts. Pool creation refuses and leaves it intact; NasBuster's
// own erase zeroes it, and the pool can then be created.
func TestLabPoolResidualFileSystem(t *testing.T) {
	l := newLabPool(t, 1)
	dev := l.loops[0]
	l.cmd("sgdisk", "-n", "1:0:0", "-t", "1:0700", dev)
	l.cmd("udevadm", "settle")
	l.cmd("mkfs.ext4", "-q", "-L", "OLDDATA", dev+"p1")
	l.cmd("sgdisk", "--zap-all", dev)
	l.cmd("udevadm", "settle")
	if r := l.role(0); r != storage.RoleAvailable {
		t.Fatalf("the disk-level view should see nothing: role = %s", r)
	}
	oldFS := func() string {
		res, _ := l.run.Run(context.Background(), "blkid", "-p", "-O", "1048576", "-s", "LABEL", "-o", "value", dev)
		return strings.TrimSpace(string(res.Stdout))
	}
	if got := oldFS(); got != "OLDDATA" {
		t.Fatalf("setup: old file system at 1 MiB = %q", got)
	}

	_, _, err := l.create(storage.LayoutSingle, 0)
	if errCode(err) != CodeDiskResidual || err.Params["found"] != "ext4" {
		t.Fatalf("create on a residual file system: %v %v", err, err.Params)
	}
	if got := oldFS(); got != "OLDDATA" {
		t.Fatalf("the refused creation touched the old file system: %q", got)
	}
	if r := l.role(0); r != storage.RoleAvailable {
		t.Fatalf("after the rollback, role = %s", r)
	}

	if err := l.wipe(0); err != nil {
		t.Fatalf("wipe: %v", err)
	}
	if got := oldFS(); got != "" {
		t.Fatalf("the erase left the old file system: %q", got)
	}
	if _, _, err := l.create(storage.LayoutSingle, 0); err != nil {
		t.Fatalf("create after the erase: %v", err)
	}
}
