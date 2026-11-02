// SPDX-License-Identifier: AGPL-3.0-or-later

package helper

import (
	"context"
	"errors"
	"io/fs"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/crosswax/nasbuster/internal/apps"
	"github.com/crosswax/nasbuster/internal/storage"
	"github.com/crosswax/nasbuster/internal/validate"
)

// diskWipe erases the partition table and every signature of a disk nobody
// uses: the way to reuse a pre-formatted (exFAT, NTFS) or retired disk.
//
// Never: the system disk, a disk outside the lab in lab mode, anything
// mounted or held by the kernel (md, LVM, dm-crypt), or a member of a CWNSB
// pool, including the surviving member of a pool that is not mounted. A
// member that a replacement made obsolete can be wiped.
func (o *Ops) diskWipe(ctx context.Context, a DiskWipeArgs) (map[string]bool, *Error) {
	if !validate.DiskID(a.ID) {
		return nil, errorf(CodeInvalidArgs, "invalid disk id")
	}
	if a.Confirm != a.ID {
		return nil, errorf(CodeConfirm, "confirmation does not match the disk id")
	}
	_ = o.check(ctx, "udevadm", "settle", "--timeout=10")
	all, err := o.Discover(ctx)
	if err != nil {
		return nil, errorf(CodeFailed, "discover disks: %v", err)
	}
	var d storage.Disk
	found := false
	for _, x := range all {
		if x.ID == a.ID {
			d, found = x, true
		}
	}
	if !found {
		return nil, errorf(CodeDiskNotFound, "no such disk: %s", a.ID)
	}
	if d.Role == storage.RoleSystem || d.Role == storage.RoleProtected {
		return nil, errorf(CodeDiskProtected, "disk %s is %s and cannot be wiped", a.ID, d.Role).
			with(map[string]any{"disk": a.ID, "role": d.Role})
	}
	if d.Transport != "loop" && d.ByIDPath == "" {
		return nil, errorf(CodeDiskProtected, "disk %s has no stable path", a.ID)
	}
	for _, u := range d.Usage {
		if u.Kind == "mounted" {
			return nil, errorf(CodeDiskMounted, "disk %s is mounted on %s", a.ID, u.Detail).
				with(map[string]any{"disk": a.ID, "mount": u.Detail})
		}
	}

	kname := path.Base(d.DevPath)
	parts := o.kernelPartitions(kname)
	for _, k := range append([]string{kname}, parts...) {
		if h := o.holder(kname, k); h != "" {
			return nil, errorf(CodeDiskBusy, "disk %s is in use by %s", a.ID, h).
				with(map[string]any{"disk": a.ID, "holder": h})
		}
	}
	for _, k := range append([]string{kname}, parts...) {
		pool, err := o.poolOwning(ctx, k)
		if err != nil {
			return nil, err // fail closed: no wipe without a clear answer
		}
		if pool != "" {
			return nil, errorf(CodeDiskInPool, "disk %s belongs to pool %s", a.ID, pool).
				with(map[string]any{"disk": a.ID, "pool": pool})
		}
	}

	for _, k := range parts {
		if err := o.check(ctx, "wipefs", "-a", "/dev/"+k); err != nil {
			return nil, err
		}
	}
	if err := o.check(ctx, "wipefs", "-a", devPath(d)); err != nil {
		return nil, err
	}
	if err := o.check(ctx, "sgdisk", "--zap-all", devPath(d)); err != nil {
		return nil, err
	}
	// Zero the start of the disk, where a pool's partition will begin (1 MiB,
	// or 8 MiB on 4Kn disks): an old file system left there by another tool's
	// wipe (partition table removed, contents kept) would otherwise reappear
	// inside the new partition and stop the pool creation.
	if err := o.check(ctx, "dd", "if=/dev/zero", "of="+devPath(d), "bs=1M", "count="+strconv.Itoa(wipeZeroMiB), "conv=fsync", "status=none"); err != nil {
		return nil, err
	}
	_ = o.check(ctx, "udevadm", "settle")
	if err := o.verifyBlank(ctx, d); err != nil {
		return nil, errorf(CodeFailed, "disk %s still not blank after wiping: %s", a.ID, err.Message)
	}
	return map[string]bool{"wiped": true}, nil
}

// kernelPartitions lists a disk's partitions as the kernel sees them (sysfs).
func (o *Ops) kernelPartitions(kname string) []string {
	entries, err := fs.ReadDir(o.FS, "sys/block/"+kname)
	if err != nil {
		return nil
	}
	var parts []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), kname) {
			parts = append(parts, e.Name())
		}
	}
	return parts
}

// holder returns what holds a disk or one of its partitions (md127, dm-0).
func (o *Ops) holder(disk, k string) string {
	dir := "sys/block/" + disk + "/holders"
	if k != disk {
		dir = "sys/block/" + disk + "/" + k + "/holders"
	}
	if h, err := fs.ReadDir(o.FS, dir); err == nil && len(h) > 0 {
		return h[0].Name()
	}
	return ""
}

// wipeZeroMiB is how much of the start of a disk an erase zeroes: enough to
// cover where any partition of a NasBuster pool begins.
const wipeZeroMiB = 16

var mdConfUUIDRe = regexp.MustCompile(`UUID=([0-9a-fA-F:]+)`)

func hexOnly(s string) string {
	return strings.ToLower(strings.NewReplacer("-", "", ":", "").Replace(s))
}

// poolOwning returns the CWNSB pool a block device (kernel name) belongs to,
// or "" when it belongs to none or is an obsolete member of a running pool.
// It fails closed: any doubt is an error, never a "no".
func (o *Ops) poolOwning(ctx context.Context, k string) (string, *Error) {
	res, err := o.Run.Run(ctx, "blkid", "-p", "-o", "export", "/dev/"+k)
	if err != nil {
		return "", errorf(CodeFailed, "probe %s: %v", k, err)
	}
	switch res.ExitCode {
	case 2:
		return "", nil // no signature: nothing that a pool could need
	case 0, 8:
	default:
		return "", errorf(CodeFailed, "probe %s: blkid exited with %d", k, res.ExitCode)
	}
	props := storage.ParseMDExport(string(res.Stdout))
	units, err := o.Files.ReadDir(o.UnitDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", errorf(CodeFailed, "list pools: %v", err)
	}
	for _, u := range units {
		if !strings.HasPrefix(u, "srv-cwnsb-") || !strings.HasSuffix(u, ".mount") {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(u, "srv-cwnsb-"), ".mount")
		switch props["TYPE"] {
		case "btrfs":
			uuid, ok := o.unitUUID(name)
			if !ok || props["UUID"] != uuid {
				continue
			}
			// Mounted: the kernel lists the live members. Not mounted, or a
			// replace running (its target may not be listed yet): keep it.
			mnt := storage.MountPoint(name)
			if m := o.mountInfo(ctx, mnt); !m.Mounted || sysExists(o.FS, "sys/fs/btrfs/"+uuid+"/devices/"+k) {
				return name, nil
			}
			if r := o.replaceStatus(ctx, mnt); r != nil && r.State == "running" {
				return name, nil
			}
		case "linux_raid_member":
			if !o.isParityPool(name) || hexOnly(props["UUID"]) != o.mdUUID(name) {
				continue
			}
			md := o.mdStatus(name)
			if md == nil || contains(md.Members, k) {
				return name, nil // array stopped (needed to assemble it) or live member
			}
		}
	}
	return "", nil
}

func sysExists(fsys fs.FS, p string) bool {
	_, err := fs.Stat(fsys, p)
	return err == nil
}

// mdUUID reads the array UUID of a parity pool from its mdadm.conf block.
func (o *Ops) mdUUID(name string) string {
	if o.Files == nil {
		return ""
	}
	b, err := o.Files.ReadFile(o.MDConf)
	if err != nil {
		return ""
	}
	begin, end := mdConfMarkers(name)
	s := string(b)
	i := strings.Index(s, begin+"\n")
	if i < 0 {
		return ""
	}
	block := s[i:]
	if j := strings.Index(block, end); j >= 0 {
		block = block[:j]
	}
	if m := mdConfUUIDRe.FindStringSubmatch(block); m != nil {
		return hexOnly(m[1])
	}
	return ""
}

// locateSeconds is how long a located disk is read.
const locateSeconds = 30

// diskLocate reads a disk for 30 seconds, bypassing the cache, so that its
// activity LED blinks: the way to find which of several identical USB disks
// is the one to pull. Reading never changes a disk; one at a time.
func (o *Ops) diskLocate(ctx context.Context, a DiskSmartArgs) (map[string]int, *Error) {
	if !validate.DiskID(a.ID) {
		return nil, errorf(CodeInvalidArgs, "invalid disk id")
	}
	all, err := o.Discover(ctx)
	if err != nil {
		return nil, errorf(CodeFailed, "discover disks: %v", err)
	}
	for _, d := range all {
		if d.ID != a.ID {
			continue
		}
		if d.Role == storage.RoleProtected || (d.Transport != "loop" && d.ByIDPath == "") {
			return nil, errorf(CodeDiskProtected, "disk %s cannot be located", a.ID)
		}
		unit := apps.JobUnit(apps.LocateJob)
		o.try(ctx, "systemctl", "stop", unit) // a new request moves the blinking
		o.try(ctx, "systemctl", "reset-failed", unit)
		args := []string{"--unit=" + strings.TrimSuffix(unit, ".service"), "--no-block", "--expand-environment=no",
			"--description=locate " + a.ID, "-p", "Type=oneshot", "-p", "RemainAfterExit=yes", "-p", "SuccessExitStatus=124",
			"/usr/bin/timeout", strconv.Itoa(locateSeconds), "/usr/bin/dd", "if=" + devPath(d), "of=/dev/null", "bs=1M", "iflag=direct", "status=none"}
		if err := o.check(ctx, "systemd-run", args...); err != nil {
			return nil, err
		}
		return map[string]int{"seconds": locateSeconds}, nil
	}
	return nil, errorf(CodeDiskNotFound, "no such disk: %s", a.ID)
}
