// SPDX-License-Identifier: AGPL-3.0-or-later

package helper

import (
	"context"
	"io/fs"
	"path"
	"strings"
	"time"

	"github.com/crosswax/nasbuster/internal/apps"
	"github.com/crosswax/nasbuster/internal/shares"
	"github.com/crosswax/nasbuster/internal/storage"
)

// A USB disk pulled out and plugged back in comes back under a new kernel name,
// and a mounted Btrfs never reopens it: the old device stays dead (scanned:
// "duplicate device"), or, mounted degraded, the devid stays without a device.
// Only a remount takes it back (ADR-0027, measured on the Pi 5).

// memberUse compares the members btrfs lists with what the mounted filesystem
// really uses (no live link under devices/). unused: devids shown present but
// not in use. back: partitions of this filesystem plugged in but not in use,
// those included. Two cases seen on the Pi 5: btrfs shows the disk under its
// new name (it is "unused"), or MISSING under its old one (blkid finds it).
func (o *Ops) memberUse(ctx context.Context, uuid string, show storage.FilesystemShow) (unused map[int]bool, back int) {
	dir := "sys/fs/btrfs/" + uuid + "/devices"
	entries, err := fs.ReadDir(o.FS, dir)
	if err != nil || len(entries) == 0 {
		return nil, 0 // unknown: never invent a problem
	}
	live := map[string]bool{}
	for _, e := range entries {
		// Stat follows the link: a removed disk leaves it dangling.
		if _, err := fs.Stat(o.FS, dir+"/"+e.Name()); err == nil {
			live[e.Name()] = true
		}
	}
	unused = map[int]bool{}
	seen := map[string]bool{}
	for _, d := range show.Devices {
		if d.Missing {
			continue
		}
		if k := o.kernelName(d.Path); k != "" && !live[k] {
			unused[d.DevID] = true
			seen[k] = true
		}
	}
	if show.MissingCount() > 0 {
		// Probed, not cached: a disk that just came back, and none that left.
		res, err := o.Run.Run(ctx, "blkid", "-c", "/dev/null", "-o", "device", "-t", "UUID="+uuid)
		if err == nil && res.ExitCode == 0 {
			for _, dev := range strings.Fields(string(res.Stdout)) {
				if k := path.Base(dev); !live[k] && !seen[k] {
					seen[k] = true
				}
			}
		}
	}
	return unused, len(seen)
}

// kernelName resolves a device path from btrfs (/dev/sdd1, or a by-id link)
// to its kernel name, "" if unknown.
func (o *Ops) kernelName(p string) string {
	if strings.HasPrefix(p, "/dev/disk/") {
		target, err := fs.ReadLink(o.FS, strings.TrimPrefix(p, "/"))
		if err != nil {
			return ""
		}
		return path.Base(target)
	}
	if !strings.HasPrefix(p, "/dev/") {
		return ""
	}
	return path.Base(p)
}

// poolResume takes back a disk that came back: the pool's SMB shares are closed
// (a client's session keeps the pool busy), the pool is remounted with all its
// disks, then the resync job converts what was written in "single" chunks and
// scrubs, so that both copies are checked again.
func (o *Ops) poolResume(ctx context.Context, a PoolResumeArgs) (PoolResumeResult, *Error) {
	var none PoolResumeResult
	if !storage.ValidPoolName(a.Name) {
		return none, errorf(CodePoolName, "invalid pool name")
	}
	if a.Confirm != a.Name {
		return none, errorf(CodeConfirm, "confirmation does not match the pool name")
	}
	for _, s := range a.Shares {
		if !shares.ValidShareName(s) {
			return none, errorf(CodeInvalidArgs, "invalid share name")
		}
	}
	st, err := o.poolStatus(ctx, PoolArgs{Name: a.Name})
	if err != nil {
		return none, err
	}
	if st.MD != nil && st.MD.Broken() {
		// Say why, rather than "nothing to take back" while the disks are there.
		return none, errorf(CodePoolBroken, "pool %s lost more disks than it tolerates: it must be assembled again as a whole", a.Name)
	}
	if !st.CanResume {
		return none, errorf(CodeNothingToResume, "no disk of pool %s is waiting to be taken back", a.Name)
	}
	if st.MD != nil {
		return o.mdResume(ctx, a.Name, st.MD)
	}
	if (st.Replace != nil && st.Replace.State == "running") || st.Balance != nil || st.Scrub.Status == "running" {
		return none, errorf(CodePoolBusy, "an operation is running on pool %s", a.Name)
	}

	for _, s := range a.Shares {
		o.try(ctx, "smbcontrol", "smbd", "close-share", s)
	}
	mnt, unit := storage.MountPoint(a.Name), storage.MountUnitName(a.Name)
	// Stopping the mount stops what requires it (Docker, when its data is on
	// the pool): those that ran are started again afterwards.
	dependents := o.activeDependents(ctx, unit)
	if err := o.checkTimeout(ctx, 2*time.Minute, "systemctl", "stop", unit); err != nil || o.mountInfo(ctx, mnt).Mounted {
		o.startUnits(ctx, dependents)
		// The unmount was refused: the pool is still mounted as before.
		if holders := append(o.fileHolders(mnt), o.mountHolders(mnt)...); len(holders) > 0 {
			return none, errorf(CodePoolInUse, "pool %s is still used by %s", a.Name, strings.Join(holders, ", ")).
				with(map[string]any{"holders": holders})
		}
		if err == nil {
			err = errorf(CodeFailed, "pool %s is still mounted", a.Name)
		}
		return none, err
	}
	// Unmounted, the filesystem accepts the disk under its new name; while
	// mounted, the kernel refused it ("duplicate device").
	o.try(ctx, "btrfs", "device", "scan")
	// The unit mounts without "degraded": it needs every disk. If that fails,
	// the pool stays unmounted on purpose: a degraded mount now could pick the
	// stale disk that just came back, if the good one dropped meanwhile. The
	// page then offers the explicit degraded mount, with its warning (ADR-0012).
	if err := o.checkTimeout(ctx, 2*time.Minute, "systemctl", "start", unit); err != nil {
		o.logger().Error("resume: the pool stays unmounted", "pool", a.Name, "err", err.Message)
		return none, err
	}
	o.startUnits(ctx, dependents)
	after, err := o.poolStatus(ctx, PoolArgs{Name: a.Name})
	if err != nil {
		return none, err
	}
	if !after.Mounted || after.MountedDegraded || after.MissingDevices > 0 {
		return none, errorf(CodeFailed, "pool %s did not take its disk back", a.Name)
	}

	out := PoolResumeResult{Remounted: true}
	var pre []string
	if after.MixedProfiles {
		data, meta := storage.TargetProfiles(after.Profiles)
		balance := btrfsBin + " balance start"
		if data != "" {
			balance += " -dconvert=" + data + ",soft"
		}
		if meta != "" {
			balance += " -mconvert=" + meta + ",soft"
		}
		pre = append(pre, balance+" "+mnt)
	}
	if err := o.startPoolJob(ctx, apps.PoolJob(apps.ResyncJob, a.Name), pre, []string{btrfsBin, "scrub", "start", "-B", mnt}); err != nil {
		return out, err
	}
	out.ResyncStarted = true
	return out, nil
}

// activeDependents lists the running services that require a mount unit
// (Docker's RequiresMountsFor): stopping the mount stops them too.
func (o *Ops) activeDependents(ctx context.Context, unit string) []string {
	res, err := o.Run.Run(ctx, "systemctl", "show", unit, "-p", "RequiredBy", "--value")
	if err != nil || res.ExitCode != 0 {
		return nil
	}
	var out []string
	for _, u := range strings.Fields(string(res.Stdout)) {
		if !strings.HasSuffix(u, ".service") {
			continue // the mounts below the pool (Docker's overlays) follow their service
		}
		if r, err := o.Run.Run(ctx, "systemctl", "is-active", u); err == nil && r.ExitCode == 0 {
			out = append(out, u)
		}
	}
	return out
}

func (o *Ops) startUnits(ctx context.Context, units []string) {
	for _, u := range units {
		o.try(ctx, "systemctl", "start", u)
	}
}

// mdReturned lists the partitions of a parity pool's array that are plugged in
// but out of the running array: a USB disk that came back. udev does not put
// them back by itself ("not adding … to active array (without --run)", seen
// in the lab), so the pool stays degraded until "Take it back". Members are
// matched by array UUID, not by name: another NAS's pool can share the name.
func (o *Ops) mdReturned(ctx context.Context, name string, md *storage.MDStatus) []string {
	want := o.mdUUID(name)
	if o.Discover == nil || want == "" {
		return nil
	}
	all, err := o.Discover(ctx)
	if err != nil {
		return nil
	}
	var out []string
	for _, d := range all {
		for _, p := range d.Partitions {
			if p.FSType == "linux_raid_member" && hexOnly(p.UUID) == want && !contains(md.Members, p.Name) {
				out = append(out, p.Name)
			}
		}
	}
	return out
}

// mdResume puts the members that came back into a parity pool's array again:
// --re-add catches up from the write-intent bitmap in seconds; --add (a full
// rebuild) when the bitmap cannot. Nothing is unmounted, no share is cut.
func (o *Ops) mdResume(ctx context.Context, name string, md *storage.MDStatus) (PoolResumeResult, *Error) {
	var none PoolResumeResult
	if md.Broken() {
		// --re-add fails on a stopped array, and the --add fallback could turn
		// the members that came back into spares: their data would be lost.
		return none, errorf(CodePoolBroken, "pool %s lost more disks than it tolerates: it must be assembled again as a whole", name)
	}
	if md.Sync != nil {
		return none, errorf(CodePoolBusy, "the array is already in %s", md.Sync.Action)
	}
	back := o.mdReturned(ctx, name, md)
	if len(back) == 0 {
		return none, errorf(CodeNothingToResume, "no disk of pool %s is waiting to be taken back", name)
	}
	// Probed again, not taken from udev: --add writes over a disk that would
	// not be a member of this very array.
	want := o.mdUUID(name)
	for _, p := range back {
		res, err := o.Run.Run(ctx, "blkid", "-p", "-o", "export", "/dev/"+p)
		if err != nil || res.ExitCode != 0 {
			return none, errorf(CodeFailed, "cannot probe %s", p)
		}
		if props := storage.ParseMDExport(string(res.Stdout)); props["TYPE"] != "linux_raid_member" || hexOnly(props["UUID"]) != want {
			return none, errorf(CodeNothingToResume, "%s is not a member of pool %s", p, name)
		}
	}
	dev := o.mdDevice(name)
	o.try(ctx, "mdadm", "--manage", dev, "--remove", "failed")
	o.try(ctx, "mdadm", "--manage", dev, "--remove", "detached")
	for _, p := range back {
		part := "/dev/" + p
		if err := o.check(ctx, "mdadm", "--manage", dev, "--re-add", part); err != nil {
			if err := o.check(ctx, "mdadm", "--manage", dev, "--add", part); err != nil {
				return none, err
			}
		}
	}
	return PoolResumeResult{ResyncStarted: true}, nil
}

// resyncRunning reports whether the pool's resync job (ADR-0027) still runs:
// a balance or a scrub started meanwhile would compete with it.
func (o *Ops) resyncRunning(ctx context.Context, pool string) bool {
	return o.job(ctx, apps.PoolJob(apps.ResyncJob, pool), 0).State == "running"
}

// fileHolders names the processes whose working directory or an open file is
// on mnt: what makes umount answer "target is busy" (a Samba session, a shell).
func (o *Ops) fileHolders(mnt string) []string {
	entries, err := fs.ReadDir(o.FS, "proc")
	if err != nil {
		return nil
	}
	under := func(p string) bool { return p == mnt || strings.HasPrefix(p, mnt+"/") }
	seen := map[string]bool{}
	var out []string
	for _, e := range entries {
		pid := e.Name()
		if pid == "" || pid[0] < '0' || pid[0] > '9' {
			continue
		}
		held := false
		if cwd, err := fs.ReadLink(o.FS, "proc/"+pid+"/cwd"); err == nil && under(cwd) {
			held = true
		} else if fds, err := fs.ReadDir(o.FS, "proc/"+pid+"/fd"); err == nil {
			for _, fd := range fds {
				if target, err := fs.ReadLink(o.FS, "proc/"+pid+"/fd/"+fd.Name()); err == nil && under(target) {
					held = true
					break
				}
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
