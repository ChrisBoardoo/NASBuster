// SPDX-License-Identifier: AGPL-3.0-or-later

package helper

import (
	"context"
	"io/fs"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/crosswax/nasbuster/internal/storage"
)

// poolDestroy removes a pool. Two cases only:
//
//   - mounted and EMPTY (its shares were deleted first): unmount, stop the
//     md array, erase the signatures of its members as the kernel lists
//     them, remove its unit and mdadm.conf block;
//   - not mounted and none of its disks present (they are gone for good):
//     forget it, touching no disk.
//
// Anything else is refused: a pool that holds data, or whose disks are
// present but not mounted, is never destroyed blindly.
func (o *Ops) poolDestroy(ctx context.Context, a PoolConfirmArgs) (PoolDestroyResult, *Error) {
	var none PoolDestroyResult
	if !storage.ValidPoolName(a.Name) {
		return none, errorf(CodePoolName, "invalid pool name")
	}
	if a.Confirm != a.Name {
		return none, errorf(CodeConfirm, "confirmation does not match the pool name")
	}
	uuid, ok := o.unitUUID(a.Name)
	if !ok {
		return none, errorf(CodePoolNotFound, "no pool named %s", a.Name)
	}
	mnt := storage.MountPoint(a.Name)
	_ = o.check(ctx, "udevadm", "settle", "--timeout=10")
	all, err := o.Discover(ctx)
	if err != nil {
		return none, errorf(CodeFailed, "discover disks: %v", err)
	}

	if !o.mountInfo(ctx, mnt).Mounted {
		if id := presentMember(all, a.Name); id != "" {
			return none, errorf(CodePoolNotMounted, "disk %s of pool %s is present: mount the pool (degraded if needed) so it can be checked empty", id, a.Name).
				with(map[string]any{"disk": id})
		}
		o.removePoolConfig(ctx, a.Name)
		return PoolDestroyResult{Forgotten: true, Disks: []string{}}, nil
	}

	entries, rerr := o.Files.ReadDir(mnt)
	if rerr != nil {
		return none, errorf(CodeFailed, "list %s: %v", mnt, rerr)
	}
	entries = slices.DeleteFunc(entries, func(e string) bool { return e == ".cwnsb" }) // configuration backups go with the pool
	if len(entries) > 0 {
		names := entries
		if len(names) > 20 {
			names = names[:20]
		}
		// Names, so that the UI can tell ".docker" (Docker's data) and ".apps"
		// (applications) from a stray folder.
		return none, errorf(CodePoolNotEmpty, "pool %s still holds %d entries: delete its shares first", a.Name, len(entries)).
			with(map[string]any{"entries": names, "count": len(entries)})
	}
	if r := o.replaceStatus(ctx, mnt); r != nil && r.State == "running" {
		return none, errorf(CodePoolBusy, "a replacement is running")
	}
	if b := o.balanceStatus(ctx, mnt); b != nil {
		return none, errorf(CodePoolBusy, "a balance is %s", b.State)
	}

	// The members, as the kernel sees them, while the pool is still mounted.
	var parts []string
	md := o.mdStatus(a.Name)
	if md != nil {
		parts = md.Members
	} else {
		names, err := fs.ReadDir(o.FS, "sys/fs/btrfs/"+uuid+"/devices")
		if err != nil {
			return none, errorf(CodeFailed, "list the pool's devices: %v", err)
		}
		for _, n := range names {
			parts = append(parts, n.Name())
		}
	}
	if len(parts) == 0 {
		return none, errorf(CodeFailed, "no member found for pool %s", a.Name)
	}
	byDev := map[string]storage.Disk{}
	for _, d := range all {
		byDev[path.Base(d.DevPath)] = d
	}
	var disks []storage.Disk
	for _, p := range parts {
		d, ok := byDev[o.parentDisk(p)]
		if !ok {
			return none, errorf(CodeFailed, "cannot find the disk holding %s", p)
		}
		disks = append(disks, d)
	}

	unit := storage.MountUnitName(a.Name)
	// Unmounting flushes the cache: more than 30 s on slow USB disks (seen in
	// the lab under load), as long as any other mount operation.
	if err := o.checkTimeout(ctx, 2*time.Minute, "systemctl", "disable", "--now", unit); err != nil {
		return none, err
	}
	if o.mountInfo(ctx, mnt).Mounted {
		return none, errorf(CodeFailed, "pool %s is still mounted", a.Name)
	}
	// Another mount namespace may still hold the filesystem (a service
	// sandbox started while the pool was mounted without shared propagation,
	// a container with a bind mount): the filesystem is then still live.
	// Put the pool back and say who holds it, rather than fail half-way.
	if holders := o.mountHolders(mnt); len(holders) > 0 {
		o.try(ctx, "systemctl", "enable", "--now", unit)
		return none, errorf(CodePoolInUse, "pool %s is still used by %s", a.Name, strings.Join(holders, ", ")).
			with(map[string]any{"holders": holders})
	}
	if md != nil {
		if err := o.check(ctx, "mdadm", "--stop", o.mdDevice(a.Name)); err != nil {
			return none, err
		}
	}
	out := PoolDestroyResult{Destroyed: true}
	for i, p := range parts {
		if err := o.check(ctx, "wipefs", "-a", "/dev/"+p); err != nil {
			return none, err
		}
		if err := o.check(ctx, "sgdisk", "--zap-all", devPath(disks[i])); err != nil {
			return none, err
		}
		out.Disks = append(out.Disks, disks[i].ID)
	}
	o.removePoolConfig(ctx, a.Name)
	_ = o.check(ctx, "udevadm", "settle")
	return out, nil
}

// mountHolders names the processes (systemd unit or command) whose mount
// namespace still has mnt or something below it mounted.
func (o *Ops) mountHolders(mnt string) []string {
	entries, err := fs.ReadDir(o.FS, "proc")
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, e := range entries {
		pid := e.Name()
		if pid == "" || pid[0] < '0' || pid[0] > '9' {
			continue
		}
		b, err := fs.ReadFile(o.FS, "proc/"+pid+"/mountinfo")
		if err != nil {
			continue
		}
		held := false
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) > 4 && (f[4] == mnt || strings.HasPrefix(f[4], mnt+"/")) {
				held = true
				break
			}
		}
		if !held {
			continue
		}
		if name := o.procLabel(pid); name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
		if len(out) >= 10 {
			break
		}
	}
	return out
}

// procLabel names a process by its systemd unit, else by its command.
func (o *Ops) procLabel(pid string) string {
	name := ""
	if cg, err := fs.ReadFile(o.FS, "proc/"+pid+"/cgroup"); err == nil {
		name = path.Base(strings.TrimSpace(string(cg))) // 0::/system.slice/wsdd2.service -> wsdd2.service
	}
	if name == "" || name == "/" || name == "." {
		if comm, err := fs.ReadFile(o.FS, "proc/"+pid+"/comm"); err == nil {
			name = strings.TrimSpace(string(comm))
		}
	}
	return name
}

// presentMember returns a disk that still carries one of the pool's
// partitions (Btrfs label cwnsb-<name>, md name <host>:cwnsb-<name>).
func presentMember(all []storage.Disk, name string) string {
	label := storage.FSLabel(name)
	for _, d := range all {
		for _, p := range d.Partitions {
			if p.Label == label || strings.HasSuffix(p.Label, ":"+label) {
				return d.ID
			}
		}
	}
	return ""
}

// parentDisk resolves a partition kernel name (sda1, loop2p1) to its disk.
func (o *Ops) parentDisk(part string) string {
	target, err := fs.ReadLink(o.FS, "sys/class/block/"+part)
	if err != nil {
		return ""
	}
	return path.Base(path.Dir(target)) // ../../devices/…/block/sda/sda1 -> sda
}

// removePoolConfig removes what the helper wrote for a pool: mount unit,
// mount point (only if empty), mdadm.conf block.
func (o *Ops) removePoolConfig(ctx context.Context, name string) {
	o.stopPoolJobs(ctx, name)
	unit := storage.MountUnitName(name)
	o.try(ctx, "systemctl", "disable", unit)
	_ = o.Files.Remove(path.Join(o.UnitDir, unit))
	o.try(ctx, "systemctl", "daemon-reload")
	_ = o.Files.Remove(storage.MountPoint(name))
	(&poolBuild{o: o, ctx: ctx, name: name}).removeMDConf()
}
