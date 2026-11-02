// SPDX-License-Identifier: AGPL-3.0-or-later

package helper

import (
	"context"
	"io/fs"
	"slices"
	"strings"
	"time"

	"github.com/crosswax/nasbuster/internal/shares"
	"github.com/crosswax/nasbuster/internal/storage"
)

// A parity pool that lost more disks at once than it tolerates (a hub cut, a
// power supply): md stops serving it ("broken"), does not take the disks back
// when they return, and after a reboot holds them in an "inactive" array (the
// survivor's superblock says the others failed). Only a forced assembly
// restarts it (ADR-0032, measured on the Pi 5 and in the lab).

// reassembleCheck reads the members of a parity pool from their own
// superblocks (mdadm --examine), never from udev: the partitions that carry
// the pool's array UUID, their roles and their event counts. nil when none
// is found. roles gives each member's place, for the probes before a write.
func (o *Ops) reassembleCheck(ctx context.Context, name string) (r *storage.Reassemble, roles map[string]int) {
	want := o.mdUUID(name)
	if want == "" || o.Discover == nil {
		return nil, nil
	}
	disks, err := o.Discover(ctx)
	if err != nil {
		return nil, nil
	}
	type member struct {
		part string
		sb   storage.MDExamine
	}
	var found []member
	for _, d := range disks {
		for _, p := range d.Partitions {
			if p.FSType != "linux_raid_member" || hexOnly(p.UUID) != want {
				continue
			}
			if sb, ok := o.examineMember(ctx, p.Name, want); ok {
				found = append(found, member{p.Name, sb})
			}
		}
	}
	if len(found) == 0 {
		return nil, nil
	}
	r = &storage.Reassemble{Level: found[0].sb.Level, Expected: found[0].sb.RaidDevices, Members: []string{}}
	roles = map[string]int{}
	freshest, stalest := found[0].sb.Events, found[0].sb.Events
	taken := map[int]bool{}
	conflict := false
	for _, m := range found {
		r.Members = append(r.Members, m.part)
		roles[m.part] = m.sb.Role
		freshest, stalest = max(freshest, m.sb.Events), min(stalest, m.sb.Events)
		// A spare, a member of another level or size of array, or two disks
		// claiming the same role (an old member replaced, then plugged back):
		// never forced.
		if m.sb.Level != r.Level || m.sb.RaidDevices != r.Expected || m.sb.Role < 0 || m.sb.Role >= r.Expected || taken[m.sb.Role] {
			conflict = true
		}
		taken[m.sb.Role] = true
	}
	for _, m := range found {
		if m.sb.Events == freshest {
			r.Fresh++
		}
	}
	slices.Sort(r.Members)
	r.EventsGap = freshest - stalest
	switch {
	case conflict || len(found) > r.Expected:
		r.Reason = storage.ReassembleRoles
	case len(found) < r.Expected:
		r.Reason = storage.ReassembleMissingDisks
	case r.EventsGap > storage.ReassembleMaxEventsGap:
		r.Reason = storage.ReassembleEventsGap
	}
	return r, roles
}

// examineMember reads the superblock of one partition (kernel name): false
// unless it belongs to the array want.
func (o *Ops) examineMember(ctx context.Context, part, want string) (storage.MDExamine, bool) {
	res, err := o.Run.Run(ctx, "mdadm", "--examine", "/dev/"+part)
	if err != nil || res.ExitCode != 0 {
		return storage.MDExamine{}, false
	}
	sb, ok := storage.ParseMDExamine(string(res.Stdout))
	return sb, ok && hexOnly(sb.ArrayUUID) == want
}

// reassembleState sets what the members' own superblocks say about a parity
// pool whose array is broken or not started.
func (o *Ops) reassembleState(ctx context.Context, name string, st *storage.PoolStatus) {
	if r, _ := o.reassembleCheck(ctx, name); r != nil {
		st.Reassemble, st.CanReassemble = r, r.Reason == ""
	}
}

// arraysHolding lists the md arrays (kernel names) that hold one of these
// partitions: the broken one, or the inactive one a reboot left.
func (o *Ops) arraysHolding(parts []string) ([]string, *Error) {
	b, err := fs.ReadFile(o.FS, "proc/mdstat")
	if err != nil {
		return nil, errorf(CodeFailed, "read /proc/mdstat: %v", err)
	}
	var out []string
	for kernel, md := range storage.ParseMDStat(string(b)) {
		if slices.ContainsFunc(md.Members, func(m string) bool { return slices.Contains(parts, m) }) {
			out = append(out, kernel)
		}
	}
	slices.Sort(out)
	return out, nil
}

// mdadmUUID writes an array UUID (32 hex digits) as mdadm prints it.
func mdadmUUID(hex string) string {
	if len(hex) != 32 {
		return hex
	}
	return hex[:8] + ":" + hex[8:16] + ":" + hex[16:24] + ":" + hex[24:]
}

// poolReassemble restarts a broken parity pool once every member is back: it
// unmounts what still points at the dead array, stops the arrays holding the
// members, assembles them by force, takes back from the bitmap those mdadm
// left out, and mounts the pool again. The parity repair it owes (writes cut
// at the failure may have left it wrong) is the scheduler's: md can only start
// it once every member is in sync, which a full rebuild may take hours to
// reach, across reboots (poolRepairParity). The daemon marks it owed from the
// status it saw before acting (resync.Apply): a failure halfway keeps it owed.
func (o *Ops) poolReassemble(ctx context.Context, a PoolReassembleArgs) (PoolReassembleResult, *Error) {
	var none PoolReassembleResult
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
	if !o.isParityPool(a.Name) {
		return none, errorf(CodeCannotReassemble, "pool %s has no parity array", a.Name).with(map[string]any{"reason": "not_broken"})
	}
	st, err := o.poolStatus(ctx, PoolArgs{Name: a.Name})
	if err != nil {
		return none, err
	}
	if !st.CanReassemble || st.Reassemble == nil {
		reason := "not_broken"
		if st.Reassemble != nil && st.Reassemble.Reason != "" {
			reason = st.Reassemble.Reason
		}
		return none, errorf(CodeCannotReassemble, "pool %s cannot be reassembled: %s", a.Name, reason).with(map[string]any{"reason": reason})
	}
	want, members := o.mdUUID(a.Name), st.Reassemble.Members
	md, mnt, unit := storage.MDDevice(a.Name), storage.MountPoint(a.Name), storage.MountUnitName(a.Name)

	// 1. A broken array serves no data: unmount what still points at it.
	var dependents []string
	if o.mountInfo(ctx, mnt).Mounted {
		for _, s := range a.Shares {
			o.try(ctx, "smbcontrol", "smbd", "close-share", s)
		}
		dependents = o.activeDependents(ctx, unit)
		if err := o.checkTimeout(ctx, 2*time.Minute, "systemctl", "stop", unit); err != nil || o.mountInfo(ctx, mnt).Mounted {
			o.startUnits(ctx, dependents)
			if holders := slices.Concat(o.fileHolders(mnt), o.mountHolders(mnt)); len(holders) > 0 {
				return none, errorf(CodePoolInUse, "pool %s is still used by %s", a.Name, strings.Join(holders, ", ")).
					with(map[string]any{"holders": holders})
			}
			if err == nil {
				err = errorf(CodeFailed, "pool %s is still mounted", a.Name)
			}
			return none, err
		}
	}
	// From here on, a failure leaves the pool unmounted: the services the
	// unmount stopped stay stopped (started now, they would write to the SD
	// card), and the answer names them.
	fail := func(err *Error) (PoolReassembleResult, *Error) {
		if len(dependents) > 0 {
			o.logger().Warn("reassemble failed: services left stopped", "pool", a.Name, "services", dependents)
			if err.Params == nil {
				err.Params = map[string]any{}
			}
			err.Params["stopped"] = dependents
		}
		return none, err
	}
	// 2. Stop the arrays that hold a member (broken, or inactive after a reboot).
	arrays, err := o.arraysHolding(members)
	if err != nil {
		return fail(err)
	}
	for _, k := range arrays {
		if err := o.check(ctx, "mdadm", "--stop", "/dev/"+k); err != nil {
			return fail(err)
		}
	}
	// The unmount may have taken minutes, and a USB disk replugged meanwhile
	// comes back under another name: read the members again before forcing.
	again, roles := o.reassembleCheck(ctx, a.Name)
	if again == nil || again.Reason != "" || !slices.Equal(again.Members, members) {
		return fail(errorf(CodeCannotReassemble, "the disks of pool %s changed during the reassembly", a.Name).
			with(map[string]any{"reason": "changed"}))
	}
	// 3. Assemble by force, from the members' own superblocks, and only from
	// those of this array (--uuid, whatever mdadm.conf says).
	args := []string{"--assemble", "--force", "--uuid=" + mdadmUUID(want), md}
	for _, m := range members {
		args = append(args, "/dev/"+m)
	}
	if err := o.checkTimeout(ctx, 2*time.Minute, "mdadm", args...); err != nil {
		return fail(err)
	}
	x := &poolBuild{o: o, ctx: ctx, name: a.Name}
	if !x.waitFor(md) {
		return fail(errorf(CodeFailed, "array %s did not appear", md))
	}
	_ = o.check(ctx, "udevadm", "settle")
	out := PoolReassembleResult{Members: members, ReAdded: []string{}, LeftOut: []string{}}

	// 4. mdadm forces only what the array needs to start (seen in the lab, with
	// members behind by different counts): the others catch up from the bitmap.
	out.ReAdded, out.LeftOut = o.takeBackLeftOut(ctx, a.Name, want, members, roles)

	// 5. Mount, and start again what the unmount stopped.
	if err := o.checkTimeout(ctx, 2*time.Minute, "systemctl", "start", unit); err != nil {
		return fail(err)
	}
	o.startUnits(ctx, dependents)
	out.Mounted = true
	return out, nil
}

// takeBackLeftOut puts back into the running array the members a forced
// assembly left out: from the bitmap, else by a full rebuild from the others.
// Each is probed again just before: --add rewrites the disk it is given, and
// a kernel name may by now point at another disk.
func (o *Ops) takeBackLeftOut(ctx context.Context, name, want string, members []string, roles map[string]int) (reAdded, leftOut []string) {
	reAdded, leftOut = []string{}, []string{}
	now := o.mdStatus(name)
	if now == nil {
		o.logger().Warn("reassemble: array state unreadable, no member taken back", "pool", name)
		return reAdded, leftOut
	}
	md := o.mdDevice(name)
	for _, m := range members {
		if contains(now.Members, m) {
			continue
		}
		if sb, ok := o.examineMember(ctx, m, want); !ok || sb.Role != roles[m] {
			o.logger().Warn("reassemble: a member changed, left out", "pool", name, "member", m)
			leftOut = append(leftOut, m)
			continue
		}
		if err := o.check(ctx, "mdadm", "--manage", md, "--re-add", "/dev/"+m); err != nil {
			if err := o.check(ctx, "mdadm", "--manage", md, "--add", "/dev/"+m); err != nil {
				o.logger().Warn("reassemble: a member stays out", "pool", name, "member", m, "err", err.Message)
				leftOut = append(leftOut, m)
				continue
			}
		}
		reAdded = append(reAdded, m)
	}
	return reAdded, leftOut
}

// poolRepairParity asks md to rewrite the parity that does not match the data
// (ADR-0032), owed after a forced assembly. md runs it in the kernel: the
// command returns at once, and the repair shows in md/sync_action. It needs
// every member in sync: during a rebuild, the kernel answers "busy".
func (o *Ops) poolRepairParity(ctx context.Context, a PoolArgs) (map[string]bool, *Error) {
	if !storage.ValidPoolName(a.Name) {
		return nil, errorf(CodePoolName, "invalid pool name")
	}
	if !o.isParityPool(a.Name) {
		return nil, errorf(CodePoolNotFound, "pool %s has no parity array", a.Name)
	}
	md, kernel := o.mdStatus(a.Name), o.mdKernel(a.Name)
	switch {
	case md == nil || kernel == "":
		return nil, errorf(CodePoolNotMounted, "the array of pool %s is not running", a.Name)
	case md.Broken():
		return nil, errorf(CodePoolBroken, "pool %s lost more disks than it tolerates: it must be assembled again as a whole", a.Name)
	case md.Degraded() || md.Sync != nil:
		return nil, errorf(CodePoolBusy, "the array of pool %s is not in sync yet", a.Name)
	case o.WriteSysfs == nil:
		return nil, errorf(CodeFailed, "cannot set the repair window of pool %s", a.Name)
	}
	// The whole array, every time: md starts a repair at sync_min, which a
	// check or a repair stopped halfway left there (Debian's monthly mdcheck
	// resumes its checks that way), and stops it at sync_max.
	for _, w := range []struct{ attr, value string }{{"sync_min", "0"}, {"sync_max", "max"}} {
		if err := o.WriteSysfs("/sys/block/"+kernel+"/md/"+w.attr, []byte(w.value)); err != nil {
			return nil, errorf(CodeFailed, "set %s of pool %s: %v", w.attr, a.Name, err)
		}
	}
	if err := o.check(ctx, "mdadm", "--action=repair", o.mdDevice(a.Name)); err != nil {
		if strings.Contains(err.Message, "resource busy") {
			return nil, errorf(CodePoolBusy, "the array of pool %s is busy", a.Name)
		}
		return nil, err
	}
	return map[string]bool{"started": true}, nil
}
