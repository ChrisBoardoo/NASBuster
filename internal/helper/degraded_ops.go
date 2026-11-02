// SPDX-License-Identifier: AGPL-3.0-or-later

package helper

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/crosswax/nasbuster/internal/apps"
	"github.com/crosswax/nasbuster/internal/storage"
	"github.com/crosswax/nasbuster/internal/validate"
)

// Disk failure handling (ADR-0012): degraded mount, replacement, redundancy
// restore and error counter reset. Same rules as pool creation: the helper
// identifies every disk itself, only ever writes to a disk it verified blank,
// and undoes its own partitioning if a later step fails.

// mountInfo is findmnt's view of a mount point.
type mountInfo struct {
	Mounted         bool
	FSType, UUID    string
	Options         string
	DegradedOptions bool
}

func (o *Ops) mountInfo(ctx context.Context, mnt string) mountInfo {
	res, err := o.Run.Run(ctx, "findmnt", "-J", "-o", "TARGET,SOURCE,FSTYPE,UUID,OPTIONS", "--mountpoint", mnt)
	if err != nil || res.ExitCode != 0 {
		return mountInfo{}
	}
	var out struct {
		Filesystems []struct {
			Target  string `json:"target"`
			FSType  string `json:"fstype"`
			UUID    string `json:"uuid"`
			Options string `json:"options"`
		} `json:"filesystems"`
	}
	if json.Unmarshal(res.Stdout, &out) != nil || len(out.Filesystems) == 0 || out.Filesystems[0].Target != mnt {
		return mountInfo{}
	}
	f := out.Filesystems[0]
	m := mountInfo{Mounted: true, FSType: f.FSType, UUID: f.UUID, Options: f.Options}
	for _, opt := range strings.Split(f.Options, ",") {
		if opt == "degraded" {
			m.DegradedOptions = true
		}
	}
	return m
}

var unitUUIDRe = regexp.MustCompile(`(?m)^What=/dev/disk/by-uuid/([0-9a-f-]{36})$`)

// unitUUID reads the filesystem UUID from the mount unit the helper wrote
// itself at creation: never from the caller.
func (o *Ops) unitUUID(name string) (string, bool) {
	b, err := o.Files.ReadFile(path.Join(o.UnitDir, storage.MountUnitName(name)))
	if err != nil {
		return "", false
	}
	m := unitUUIDRe.FindSubmatch(b)
	if m == nil || !fsUUIDRe.Match(m[1]) {
		return "", false
	}
	return string(m[1]), true
}

// isParityPool reports whether mdadm.conf holds this pool's array block.
func (o *Ops) isParityPool(name string) bool {
	b, err := o.Files.ReadFile(o.MDConf)
	if err != nil {
		return false
	}
	begin, _ := mdConfMarkers(name)
	return strings.Contains(string(b), begin+"\n")
}

func (o *Ops) btrfsShow(ctx context.Context, target string) (storage.FilesystemShow, bool) {
	res, err := o.Run.Run(ctx, "btrfs", "filesystem", "show", "--raw", target)
	if err != nil || res.ExitCode != 0 {
		return storage.FilesystemShow{}, false
	}
	return storage.ParseFilesystemShow(string(res.Stdout)), true
}

func (o *Ops) replaceStatus(ctx context.Context, mnt string) *storage.ReplaceStatus {
	res, err := o.Run.Run(ctx, "btrfs", "replace", "status", "-1", mnt)
	if err != nil || res.ExitCode != 0 {
		return nil
	}
	return storage.ParseReplaceStatus(string(res.Stdout))
}

func (o *Ops) balanceStatus(ctx context.Context, mnt string) *storage.BalanceStatus {
	res, err := o.Run.Run(ctx, "btrfs", "balance", "status", mnt)
	if err != nil || (res.ExitCode != 0 && res.ExitCode != 1) { // 1: running or paused
		return nil
	}
	return storage.ParseBalanceStatus(string(res.Stdout))
}

// failureState adds the ADR-0012 facts to a pool status.
func (o *Ops) failureState(ctx context.Context, name string, m mountInfo, st *storage.PoolStatus) {
	mnt := storage.MountPoint(name)
	if md := st.MD; md != nil {
		// Missing: not in the array at all. A member being rebuilt is listed
		// (not active yet), a faulty one too (counted apart, in Faulty).
		if n := md.Expected - len(md.Members); n > 0 {
			st.MissingDevices = n
		}
		// A member back under a new name, out of the array (ADR-0027). Not
		// for a broken array: its members go back all together, not one by one.
		if md.Degraded() && !md.Broken() {
			if back := o.mdReturned(ctx, name, md); len(back) > 0 && len(back) >= st.MissingDevices+len(md.Faulty) {
				st.CanResume = true
			}
		}
		if md.Broken() {
			o.reassembleState(ctx, name, st) // ADR-0032
		}
	}
	if !m.Mounted {
		// Why is it not mounted? A missing disk is the common case on USB.
		if o.isParityPool(name) {
			started := o.mdKernel(name) != ""
			st.CanMountDegraded = !started
			if !started {
				// One disk missing at boot: a degraded mount runs the array with
				// the others. Every disk back but behind (a hub cut): only a
				// forced assembly does; a degraded mount would fail (ADR-0032).
				o.reassembleState(ctx, name, st)
				if r := st.Reassemble; r != nil {
					st.CanMountDegraded = !st.CanReassemble && r.Fresh >= r.Expected-storage.MDTolerance(r.Level, r.Expected)
				}
			}
			return
		}
		if uuid, ok := o.unitUUID(name); ok {
			if show, ok := o.btrfsShow(ctx, uuid); ok {
				st.MissingDevices = show.MissingCount()
				st.CanMountDegraded = st.MissingDevices > 0
				for _, d := range show.Devices {
					if !d.Missing {
						st.Devices = append(st.Devices, storage.PoolDevice{Path: d.Path, DevID: d.DevID, SizeBytes: d.SizeBytes})
					}
				}
			}
		}
		return
	}
	if m.FSType != "btrfs" {
		return
	}
	st.MountedDegraded = m.DegradedOptions
	if show, ok := o.btrfsShow(ctx, mnt); ok {
		// Parity pools: Btrfs sees one md device; md's own count (above) wins.
		st.MissingDevices = max(st.MissingDevices, show.MissingCount())
		var unused map[int]bool
		back := 0
		if st.MD == nil {
			unused, back = o.memberUse(ctx, st.UUID, show)
		}
		for i := range st.Devices {
			for _, d := range show.Devices {
				if d.DevID == st.Devices[i].DevID {
					st.Devices[i].SizeBytes, st.Devices[i].Missing = d.SizeBytes, d.Missing
				}
			}
			// The kernel's own flag (sysfs) wins when present.
			p := fmt.Sprintf("sys/fs/btrfs/%s/devinfo/%d/missing", st.UUID, st.Devices[i].DevID)
			if b, err := fs.ReadFile(o.FS, p); err == nil && strings.TrimSpace(string(b)) == "1" {
				st.Devices[i].Missing = true
			}
			// Plugged back in, but the mounted filesystem does not use it.
			if unused[st.Devices[i].DevID] {
				st.Devices[i].Missing = true
			}
		}
		// Every missing member plugged in again: a remount takes them back (ADR-0027).
		if missing := show.MissingCount() + len(unused); missing > 0 {
			st.MissingDevices = max(st.MissingDevices, missing)
			st.CanResume = back >= missing
		}
	}
	// One missing device: its size is the "Device missing" total (btrfs shows 0).
	var missing []int
	for i := range st.Devices {
		if st.Devices[i].Missing {
			missing = append(missing, i)
		}
	}
	if len(missing) == 1 && st.Devices[missing[0]].SizeBytes == 0 {
		st.Devices[missing[0]].SizeBytes = st.Missing
	}
	st.MixedProfiles = storage.MixedProfiles(st.Profiles)
	st.Replace = o.replaceStatus(ctx, mnt)
	st.Balance = o.balanceStatus(ctx, mnt)
}

// checkTimeout is check with a custom deadline (mount, assemble).
func (o *Ops) checkTimeout(ctx context.Context, d time.Duration, name string, args ...string) *Error {
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	res, err := o.Run.Run(ctx, name, args...)
	if err != nil {
		return errorf(CodeFailed, "%s: %v", name, err)
	}
	if res.ExitCode != 0 {
		return errorf(CodeFailed, "%s exited with %d: %s", name, res.ExitCode, firstLine(res.Stderr))
	}
	return nil
}

// poolMountDegraded mounts a pool that lacks a disk. Never automatic: a USB
// disk that comes and goes could otherwise lead to two diverging copies.
func (o *Ops) poolMountDegraded(ctx context.Context, a PoolConfirmArgs) (map[string]bool, *Error) {
	if !storage.ValidPoolName(a.Name) {
		return nil, errorf(CodePoolName, "invalid pool name")
	}
	if a.Confirm != a.Name {
		return nil, errorf(CodeConfirm, "confirmation does not match the pool name")
	}
	mnt := storage.MountPoint(a.Name)
	if o.mountInfo(ctx, mnt).Mounted {
		return nil, errorf(CodePoolMounted, "pool %s is already mounted", a.Name)
	}
	uuid, ok := o.unitUUID(a.Name)
	if !ok {
		return nil, errorf(CodePoolNotFound, "no pool named %s", a.Name)
	}
	_ = o.check(ctx, "udevadm", "settle", "--timeout=10")

	if o.isParityPool(a.Name) {
		md := storage.MDDevice(a.Name)
		if o.mdKernel(a.Name) != "" {
			return nil, errorf(CodeNothingMissing, "the array is running: the pool can be mounted normally")
		}
		// At boot, the initramfs may hold the members in an inactive array
		// named after their superblock: it would keep them from assembling.
		if r, _ := o.reassembleCheck(ctx, a.Name); r != nil {
			arrays, err := o.arraysHolding(r.Members)
			if err != nil {
				return nil, err
			}
			for _, k := range arrays {
				if err := o.check(ctx, "mdadm", "--stop", "/dev/"+k); err != nil {
					return nil, err
				}
			}
		}
		if err := o.checkTimeout(ctx, 2*time.Minute, "mdadm", "--assemble", "--run", md); err != nil {
			return nil, err
		}
		x := &poolBuild{o: o, ctx: ctx, name: a.Name}
		if !x.waitFor(md) {
			return nil, errorf(CodeFailed, "array %s did not appear", md)
		}
		_ = o.check(ctx, "udevadm", "settle")
		if err := o.checkTimeout(ctx, 2*time.Minute, "systemctl", "start", storage.MountUnitName(a.Name)); err != nil {
			return nil, err
		}
	} else {
		show, ok := o.btrfsShow(ctx, uuid)
		if !ok {
			return nil, errorf(CodeDiskNotFound, "no disk of pool %s is present", a.Name)
		}
		if show.MissingCount() == 0 {
			return nil, errorf(CodeNothingMissing, "every disk is present: the pool can be mounted normally")
		}
		if err := o.checkTimeout(ctx, 2*time.Minute, "mount", "-t", "btrfs", "-o", "noatime,compress=zstd:1,degraded",
			"/dev/disk/by-uuid/"+uuid, mnt); err != nil {
			return nil, err
		}
	}
	if m := o.mountInfo(ctx, mnt); !m.Mounted || m.UUID != uuid {
		return nil, errorf(CodeFailed, "pool not mounted as expected")
	}
	return map[string]bool{"mounted": true}, nil
}

// poolReplace puts a blank disk in place of a missing, faulty or failing one.
func (o *Ops) poolReplace(ctx context.Context, a PoolReplaceArgs) (PoolReplaceResult, *Error) {
	var none PoolReplaceResult
	if !storage.ValidPoolName(a.Name) {
		return none, errorf(CodePoolName, "invalid pool name")
	}
	if a.Confirm != a.Name {
		return none, errorf(CodeConfirm, "confirmation does not match the pool name")
	}
	if !validate.DiskID(a.Old) || !validate.DiskID(a.New) || a.Old == a.New {
		return none, errorf(CodeInvalidArgs, "invalid disk ids")
	}
	mnt := storage.MountPoint(a.Name)
	if m := o.mountInfo(ctx, mnt); !m.Mounted || m.FSType != "btrfs" {
		return none, errorf(CodePoolNotMounted, "pool %s must be mounted (degraded if needed) to replace a disk", a.Name)
	}
	if md := o.mdStatus(a.Name); md != nil && md.Broken() {
		// A disk added now would only be a spare: the array cannot rebuild
		// onto it, and it would stand in the way of the reassembly (ADR-0032).
		return none, errorf(CodePoolBroken, "pool %s lost more disks than it tolerates: it must be assembled again as a whole", a.Name)
	}

	_ = o.check(ctx, "udevadm", "settle", "--timeout=10")
	all, err := o.Discover(ctx)
	if err != nil {
		return none, errorf(CodeFailed, "discover disks: %v", err)
	}
	byID := map[string]storage.Disk{}
	for _, d := range all {
		byID[d.ID] = d
	}
	nd, ok := byID[a.New]
	if !ok {
		return none, errorf(CodeDiskNotFound, "no such disk: %s", a.New)
	}
	if nd.Role != storage.RoleAvailable || (nd.Transport != "loop" && nd.ByIDPath == "") {
		return none, errorf(CodeDiskNotAvailable, "disk %s is %s, only a blank disk can replace a member", a.New, nd.Role).
			with(map[string]any{"disk": a.New, "role": nd.Role})
	}
	old, oldPresent := byID[a.Old]

	if md := o.mdStatus(a.Name); md != nil {
		return o.replaceMD(ctx, a, md, nd, old, oldPresent)
	}
	return o.replaceBtrfs(ctx, a, mnt, nd, old, oldPresent)
}

// partKernel is the kernel name of a disk's first partition (sda1, loop2p1).
func (o *Ops) partKernel(d storage.Disk) string {
	p := partPath(d)
	if d.Transport == "loop" {
		return path.Base(p)
	}
	target, err := fs.ReadLink(o.FS, strings.TrimPrefix(p, "/"))
	if err != nil {
		return ""
	}
	return path.Base(target)
}

func (o *Ops) replaceBtrfs(ctx context.Context, a PoolReplaceArgs, mnt string, nd, old storage.Disk, oldPresent bool) (PoolReplaceResult, *Error) {
	var none PoolReplaceResult
	if r := o.replaceStatus(ctx, mnt); r != nil && r.State == "running" {
		return none, errorf(CodePoolBusy, "a replacement is already running")
	}
	if b := o.balanceStatus(ctx, mnt); b != nil {
		return none, errorf(CodePoolBusy, "a balance is %s", b.State)
	}
	show, ok := o.btrfsShow(ctx, mnt)
	if !ok {
		return none, errorf(CodeFailed, "read the pool's devices")
	}

	// Which devid goes away: the old disk's partition if it is still there
	// (failing disk), otherwise the one missing devid.
	devid, need, readSource := 0, uint64(0), false
	if oldPresent {
		if k := o.partKernel(old); k != "" {
			if id, ok := show.DevIDOf(k); ok {
				devid, readSource = id, true
				for _, d := range show.Devices {
					if d.DevID == id {
						need = d.SizeBytes
					}
				}
			}
		}
	}
	if devid == 0 {
		ids := show.MissingDevIDs()
		if len(ids) != 1 {
			return none, errorf(CodeMemberNotFound, "cannot tell which device %s was (%d missing)", a.Old, len(ids))
		}
		devid = ids[0]
		if res, err := o.Run.Run(ctx, "btrfs", "filesystem", "usage", "-b", mnt); err == nil {
			_, _, _, need = storage.ParseFilesystemUsage(string(res.Stdout)) // one missing device: its size
		}
	}
	if storage.PartitionBytes(nd.SizeBytes) < need {
		return none, errorf(CodeDiskTooSmall, "disk %s is too small to replace %s", a.New, a.Old).
			with(map[string]any{"disk": a.New, "requiredBytes": need})
	}
	if err := o.verifyBlank(ctx, nd); err != nil {
		return none, err
	}

	x := &poolBuild{o: o, ctx: ctx, name: a.Name}
	part, err := x.partitionOne(nd, "8300")
	if err != nil {
		x.rollback()
		return none, err
	}
	// Hours on a big disk: in its own unit, so that neither the helper's idle
	// exit nor a package upgrade stops it (ADR-0026).
	args := []string{btrfsBin, "replace", "start", "-B"}
	if readSource {
		args = append(args, "-r") // read the failing disk only when no good copy exists
	}
	args = append(args, strconv.Itoa(devid), part, mnt)
	// btrfs replace writes the new superblock without a udev event: until one
	// comes, udev (lsblk) shows the new member without its file system (seen on
	// the Pi 5). "-": a failure here must not turn a good replace into a failed job.
	post := []string{"-/usr/bin/udevadm trigger --action=change " + part}
	if err := o.startPoolJobThen(ctx, apps.PoolJob(apps.ReplaceJob, a.Name), nil, post, args); err != nil {
		o.logger().Error("replace failed, rolling back", "pool", a.Name, "err", err.Message)
		x.rollback()
		return none, err
	}
	return PoolReplaceResult{Old: a.Old, New: a.New, DevID: devid, Method: "btrfs-replace"}, nil
}

func (o *Ops) replaceMD(ctx context.Context, a PoolReplaceArgs, md *storage.MDStatus, nd, old storage.Disk, oldPresent bool) (PoolReplaceResult, *Error) {
	var none PoolReplaceResult
	if md.Sync != nil {
		return none, errorf(CodePoolBusy, "the array is already in %s", md.Sync.Action)
	}
	kernel := o.mdKernel(a.Name)
	oldPart := ""
	if oldPresent {
		if k := o.partKernel(old); k != "" && contains(md.Members, k) {
			if !contains(md.Faulty, k) {
				return none, errorf(CodeMemberHealthy, "%s is an active member: only a missing or faulty member can be replaced", a.Old)
			}
			oldPart = "/dev/" + k
		}
	}
	if oldPart == "" && !md.Degraded() {
		return none, errorf(CodeMemberNotFound, "no free slot in the array for %s", a.New)
	}

	// The new partition must hold the member's data offset plus its data.
	need := uint64(0)
	if b, err := fs.ReadFile(o.FS, "sys/block/"+kernel+"/md/component_size"); err == nil {
		kib, _ := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
		need = kib << 10
		for _, m := range md.Members {
			if contains(md.Faulty, m) {
				continue
			}
			if b, err := fs.ReadFile(o.FS, "sys/block/"+kernel+"/md/dev-"+m+"/offset"); err == nil {
				sectors, _ := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
				need += sectors * 512
				break
			}
		}
	}
	if storage.PartitionBytes(nd.SizeBytes) < need {
		return none, errorf(CodeDiskTooSmall, "disk %s is too small to replace %s", a.New, a.Old).
			with(map[string]any{"disk": a.New, "requiredBytes": need})
	}
	if err := o.verifyBlank(ctx, nd); err != nil {
		return none, err
	}

	x := &poolBuild{o: o, ctx: ctx, name: a.Name}
	part, err := x.partitionOne(nd, "FD00")
	if err != nil {
		x.rollback()
		return none, err
	}
	dev := o.mdDevice(a.Name)
	if oldPart != "" {
		o.try(ctx, "mdadm", "--manage", dev, "--remove", oldPart)
	}
	o.try(ctx, "mdadm", "--manage", dev, "--remove", "failed")
	o.try(ctx, "mdadm", "--manage", dev, "--remove", "detached")
	if err := o.check(ctx, "mdadm", "--manage", dev, "--add", part); err != nil {
		o.logger().Error("md add failed, rolling back", "pool", a.Name, "err", err.Message)
		x.rollback()
		return none, err
	}
	return PoolReplaceResult{Old: a.Old, New: a.New, Method: "md-add"}, nil
}

// mdKernel finds the running md array of a pool and gives its kernel name
// (md127), "" when it does not run. By the array UUID first (udev's
// /dev/disk/by-id/md-uuid-…): at boot, the initramfs assembles arrays with
// the mdadm.conf it was built with, which may predate the pool, and names it
// after its superblock (/dev/md/rpi5-nas:cwnsb-e2, seen on the Pi 5). Then
// by /dev/md/cwnsb-<name>, the name NasBuster gives it.
func (o *Ops) mdKernel(name string) string {
	links := []string{storage.MDDevice(name)}
	if uuid := o.mdUUID(name); len(uuid) == 32 {
		links = append([]string{"/dev/disk/by-id/md-uuid-" + mdadmUUID(uuid)}, links...)
	}
	for _, link := range links {
		if target, err := fs.ReadLink(o.FS, strings.TrimPrefix(link, "/")); err == nil {
			return path.Base(target) // ../md127 -> md127
		}
	}
	return ""
}

// mdDevice is the device to give mdadm for a pool's running array:
// /dev/md/cwnsb-<name> when that link points at it, else its kernel device
// (/dev/md127), whatever udev named it.
func (o *Ops) mdDevice(name string) string {
	md, k := storage.MDDevice(name), o.mdKernel(name)
	if k == "" {
		return md
	}
	if target, err := fs.ReadLink(o.FS, strings.TrimPrefix(md, "/")); err == nil && path.Base(target) == k {
		return md
	}
	return "/dev/" + k
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// partitionOne gives a verified-blank disk the pool's partition layout.
func (x *poolBuild) partitionOne(d storage.Disk, partType string) (string, *Error) {
	o := x.o
	if err := o.check(x.ctx, "sgdisk", "-n", "1:0:-100M", "-t", "1:"+partType, "-c", "1:"+storage.FSLabel(x.name), devPath(d)); err != nil {
		return "", err
	}
	x.partitioned = append(x.partitioned, d)
	_ = o.check(x.ctx, "udevadm", "settle")
	p := partPath(d)
	if !x.waitFor(p) {
		return "", errorf(CodeFailed, "partition %s did not appear", p)
	}
	return p, nil
}

// poolRestoreRedundancy grows every device to its partition and converts the
// chunks written while degraded back to the pool's redundant profiles.
func (o *Ops) poolRestoreRedundancy(ctx context.Context, a PoolArgs) (RestoreResult, *Error) {
	var none RestoreResult
	if !storage.ValidPoolName(a.Name) {
		return none, errorf(CodePoolName, "invalid pool name")
	}
	mnt := storage.MountPoint(a.Name)
	if m := o.mountInfo(ctx, mnt); !m.Mounted || m.FSType != "btrfs" {
		return none, errorf(CodePoolNotMounted, "pool %s is not mounted", a.Name)
	}
	show, ok := o.btrfsShow(ctx, mnt)
	if !ok {
		return none, errorf(CodeFailed, "read the pool's devices")
	}
	if show.MissingCount() > 0 {
		return none, errorf(CodeDiskMissing, "a disk is still missing: replace it first")
	}
	if r := o.replaceStatus(ctx, mnt); r != nil && r.State == "running" {
		return none, errorf(CodePoolBusy, "a replacement is running")
	}
	if b := o.balanceStatus(ctx, mnt); b != nil {
		return none, errorf(CodePoolBusy, "a balance is %s", b.State)
	}
	if o.resyncRunning(ctx, a.Name) {
		return none, errorf(CodePoolBusy, "the pool is being resynchronized")
	}
	for _, d := range show.Devices {
		if err := o.btrfsExclusive(ctx, "filesystem", "resize", strconv.Itoa(d.DevID)+":max", mnt); err != nil {
			return none, err
		}
	}
	res, err := o.Run.Run(ctx, "btrfs", "--format", "json", "filesystem", "df", mnt)
	if err != nil || res.ExitCode != 0 {
		return none, errorf(CodeFailed, "read the pool's profiles")
	}
	df, perr := storage.ParseFilesystemDF(res.Stdout)
	if perr != nil {
		return none, errorf(CodeFailed, "%v", perr)
	}
	out := RestoreResult{Resized: len(show.Devices)}
	if !storage.MixedProfiles(df) {
		return out, nil
	}
	data, meta := storage.TargetProfiles(df)
	args := []string{btrfsBin, "balance", "start"}
	if data != "" {
		args = append(args, "-dconvert="+data+",soft")
	}
	if meta != "" {
		args = append(args, "-mconvert="+meta+",soft")
	}
	if err := o.startExclusivePoolJob(ctx, apps.PoolJob(apps.BalanceJob, a.Name), append(args, mnt)); err != nil {
		return none, err
	}
	out.BalanceStarted, out.Data, out.Metadata = true, data, meta
	return out, nil
}

// clearErrorsMaxAge: the scrub that proves the pool clean must be recent.
const clearErrorsMaxAge = 48 * time.Hour

// poolClearErrors resets the Btrfs error counters, but only once a recent
// scrub has finished without uncorrectable errors: a problem is never hidden
// before it has been checked.
func (o *Ops) poolClearErrors(ctx context.Context, a PoolArgs) (map[string]bool, *Error) {
	if !storage.ValidPoolName(a.Name) {
		return nil, errorf(CodePoolName, "invalid pool name")
	}
	mnt := storage.MountPoint(a.Name)
	if m := o.mountInfo(ctx, mnt); !m.Mounted || m.FSType != "btrfs" {
		return nil, errorf(CodePoolNotMounted, "pool %s is not mounted", a.Name)
	}
	res, err := o.Run.Run(ctx, "btrfs", "scrub", "status", "-R", mnt)
	if err != nil || res.ExitCode != 0 {
		return nil, errorf(CodeScrubRequired, "run a scrub first")
	}
	s := storage.ParseScrubStatus(string(res.Stdout))
	now := time.Now()
	if o.Now != nil {
		now = o.Now()
	}
	if s.Status != "finished" || s.Uncorrectable > 0 || s.StartedAt == 0 || now.Sub(time.Unix(s.StartedAt, 0)) > clearErrorsMaxAge {
		return nil, errorf(CodeScrubRequired, "a scrub must finish without uncorrectable errors first").
			with(map[string]any{"scrubStatus": s.Status, "uncorrectable": s.Uncorrectable})
	}
	if err := o.check(ctx, "btrfs", "device", "stats", "-z", mnt); err != nil {
		return nil, err
	}
	return map[string]bool{"cleared": true}, nil
}

// btrfsExclusive runs a btrfs command that needs the filesystem's exclusive
// operation slot (resize, balance). Right after "replace status" says
// finished, the kernel may still hold the slot for the replace (seen in the
// lab): retry briefly, then report the pool busy.
func (o *Ops) btrfsExclusive(ctx context.Context, args ...string) *Error {
	for try := 0; ; try++ {
		res, err := o.Run.Run(ctx, "btrfs", args...)
		if err != nil {
			return errorf(CodeFailed, "btrfs: %v", err)
		}
		if res.ExitCode == 0 {
			return nil
		}
		if !strings.Contains(string(res.Stderr), "another exclusive operation") {
			return errorf(CodeFailed, "btrfs exited with %d: %s", res.ExitCode, firstLine(res.Stderr))
		}
		if try == 14 {
			return errorf(CodePoolBusy, "another Btrfs operation is still finishing")
		}
		o.sleep(time.Second)
	}
}
