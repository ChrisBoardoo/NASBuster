// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build integration && linux

// Real-kernel tests of disk failure handling (ADR-0012), in the cwnsb-lab
// distro only (see pool_integration_test.go for how to run them).
package helper

import (
	"bytes"
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/crosswax/nasbuster/internal/storage"
)

func (l *labPool) cmd(name string, args ...string) {
	l.t.Helper()
	res, err := l.run.Run(context.Background(), name, args...)
	if err != nil || res.ExitCode != 0 {
		l.t.Fatalf("%s %v: %v %s", name, args, err, res.Stderr)
	}
}

// unplug detaches a loop as if its USB disk had been pulled.
func (l *labPool) unplug(i int) {
	l.t.Helper()
	l.cmd("losetup", "-d", l.loops[i])
	l.loops[i] = "" // never touched again, even by cleanup
	l.run.Run(context.Background(), "udevadm", "settle")
}

func (l *labPool) status(name string) storage.PoolStatus {
	l.t.Helper()
	st, err := l.ops.poolStatus(context.Background(), PoolArgs{Name: name})
	if err != nil {
		l.t.Fatalf("status: %v", err)
	}
	return st
}

// waitStatus polls the pool until cond holds (or fails after a minute).
func (l *labPool) waitStatus(name, what string, cond func(storage.PoolStatus) bool) storage.PoolStatus {
	l.t.Helper()
	for i := 0; i < 240; i++ {
		st := l.status(name)
		if cond(st) {
			return st
		}
		time.Sleep(250 * time.Millisecond)
	}
	st := l.status(name)
	l.t.Fatalf("timeout waiting for %s: %+v replace=%+v balance=%+v md=%+v", what, st, st.Replace, st.Balance, st.MD)
	return st
}

func randomFile(t *testing.T, p string, size int) []byte {
	t.Helper()
	b := make([]byte, size)
	rand.Read(b)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return b
}

func sameContent(t *testing.T, p string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(p)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("%s changed or unreadable: %v", p, err)
	}
}

func errCode(err *Error) string {
	if err == nil {
		return ""
	}
	return err.Code
}

// dropCaches makes the next read come from the disks, not from memory.
func dropCaches(t *testing.T) {
	t.Helper()
	if err := os.WriteFile("/proc/sys/vm/drop_caches", []byte("3"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A hub cut (ADR-0032, seen on the Pi 5): two of three members leave at once,
// then come back. md keeps the array broken, and leaves it inactive after a
// reboot; the product restarts it by force, in both cases.
func TestLabParityReassembleAfterHubCut(t *testing.T) {
	ctx := context.Background()
	l := newLabPool(t, 3)
	name, res, err := l.create(storage.LayoutParity1, 0, 1, 2)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	l.waitStatus(name, "initial resync", func(s storage.PoolStatus) bool { return s.MD != nil && s.MD.Sync == nil })
	data := randomFile(t, filepath.Join(res.MountPoint, "data.bin"), 24<<20)
	l.cmd("sync")
	file := filepath.Join(res.MountPoint, "data.bin")
	cut := func() {
		for _, i := range []int{1, 2} {
			l.cmd("mdadm", "-If", strings.TrimPrefix(l.loops[i], "/dev/")+"p1") // what udev runs when the hub drops
		}
	}
	// What the scheduler does next (ADR-0032): once md has every member in
	// sync, the parity repair, then the wait for its end.
	repair := func(stage string) {
		l.waitStatus(name, stage+": every member in sync", func(s storage.PoolStatus) bool {
			return s.Mounted && s.MD != nil && s.MD.Sync == nil && !s.MD.Degraded()
		})
		if _, err := l.ops.poolRepairParity(ctx, PoolArgs{Name: name}); err != nil {
			t.Fatalf("%s: repair: %v", stage, err)
		}
		l.waitStatus(name, stage+": repaired", func(s storage.PoolStatus) bool { return s.MD != nil && s.MD.Sync == nil })
	}

	// 1. Hot: the array is broken but still mounted, the members are back.
	cut()
	st := l.status(name)
	if st.MD == nil || !st.MD.Broken() || st.CanResume || !st.CanReassemble || len(st.Reassemble.Members) != 3 || st.Reassemble.Reason != "" {
		t.Fatalf("broken, members back = %+v md=%+v r=%+v", st, st.MD, st.Reassemble)
	}
	if _, err := l.ops.poolResume(ctx, PoolResumeArgs{Name: name, Confirm: name}); errCode(err) != CodePoolBroken {
		t.Fatalf("resume on a broken array: %v", err)
	}
	if _, err := l.ops.poolRepairParity(ctx, PoolArgs{Name: name}); errCode(err) != CodePoolBroken {
		t.Fatalf("repair a broken array: %v", err)
	}
	out, err := l.ops.poolReassemble(ctx, PoolReassembleArgs{Name: name, Confirm: name})
	if err != nil || !out.Mounted || len(out.Members) != 3 {
		t.Fatalf("hot reassemble = %+v %v", out, err)
	}
	repair("hot")
	dropCaches(t)
	sameContent(t, file, data)

	// 2. After a reboot: the same cut, then udev's incremental assembly holds
	// every member in an inactive array that a normal assembly cannot start.
	cut()
	l.cmd("systemctl", "stop", storage.MountUnitName(name))
	l.cmd("mdadm", "--stop", storage.MDDevice(name))
	for i := range 3 {
		l.run.Run(ctx, "mdadm", "--incremental", l.loops[i]+"p1") // "not enough to start"
	}
	st = l.status(name)
	if st.Mounted || !st.CanReassemble || st.CanMountDegraded || st.Reassemble.EventsGap == 0 {
		t.Fatalf("after a reboot = %+v r=%+v", st, st.Reassemble)
	}
	out, err = l.ops.poolReassemble(ctx, PoolReassembleArgs{Name: name, Confirm: name})
	if err != nil || !out.Mounted || len(out.Members) != 3 {
		t.Fatalf("reassemble after a reboot = %+v %v", out, err)
	}
	repair("after a reboot")
	dropCaches(t)
	sameContent(t, file, data)

	// 3. A healthy pool is never assembled by force.
	if _, err := l.ops.poolReassemble(ctx, PoolReassembleArgs{Name: name, Confirm: name}); errCode(err) != CodeCannotReassemble {
		t.Fatalf("reassemble a healthy pool: %v", err)
	}
}

// A parity repair stopped halfway keeps its place in md/sync_min, and one that
// reaches its end sets it back to 0 (ADR-0032): one of the signs that tell the
// scheduler whether the repair it started is done. md records the place at
// its checkpoints (every 1/16 of the array): the repair is bridled, then
// stopped past the first one.
func TestLabParityRepairStoppedHalfway(t *testing.T) {
	ctx := context.Background()
	l := newLabPool(t, 3)
	name, _, err := l.create(storage.LayoutParity1, 0, 1, 2)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	l.waitStatus(name, "initial resync", func(s storage.PoolStatus) bool { return s.MD != nil && s.MD.Sync == nil })
	md := "/sys/block/" + l.ops.mdKernel(name) + "/md/"
	write := func(attr, value string) {
		t.Helper()
		if err := os.WriteFile(md+attr, []byte(value), 0o644); err != nil {
			t.Fatalf("%s: %v", attr, err)
		}
	}
	write("sync_speed_max", "4000") // KiB/s: minutes for the lab's array
	t.Cleanup(func() { os.WriteFile(md+"sync_speed_max", []byte("system"), 0o644) })

	if _, err := l.ops.poolRepairParity(ctx, PoolArgs{Name: name}); err != nil {
		t.Fatalf("repair: %v", err)
	}
	if st := l.status(name); st.MD.Sync == nil || st.MD.Sync.Action != "repair" {
		t.Fatalf("repair not seen: %+v", st.MD)
	}
	// Past the first checkpoint ("done / total" in sectors).
	deadline := time.Now().Add(2 * time.Minute)
	for {
		b, _ := os.ReadFile(md + "sync_completed")
		f := strings.Fields(string(b))
		if len(f) == 3 {
			done, _ := strconv.ParseInt(f[0], 10, 64)
			total, _ := strconv.ParseInt(f[2], 10, 64)
			if total > 0 && done > total/16+total/64 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("repair stuck: sync_completed %q", b)
		}
		time.Sleep(time.Second)
	}
	write("sync_action", "idle")
	st := l.waitStatus(name, "repair stopped", func(s storage.PoolStatus) bool { return s.MD != nil && s.MD.Sync == nil })
	if b, _ := os.ReadFile(md + "sync_min"); !st.MD.Interrupted {
		t.Fatalf("a repair stopped halfway is not seen: sync_min %q", b)
	}

	// Started again (over the whole array: sync_min set back to 0), to its end.
	write("sync_speed_max", "system")
	if _, err := l.ops.poolRepairParity(ctx, PoolArgs{Name: name}); err != nil {
		t.Fatalf("repair again: %v", err)
	}
	st = l.waitStatus(name, "repaired", func(s storage.PoolStatus) bool { return s.MD != nil && s.MD.Sync == nil })
	if b, _ := os.ReadFile(md + "sync_min"); st.MD.Interrupted {
		t.Fatalf("a repair at its end left sync_min %q", b)
	}
}

func TestLabMirrorMissingDiskReplace(t *testing.T) {
	ctx := context.Background()
	l := newLabPool(t, 3)
	name, res, err := l.create(storage.LayoutMirror, 0, 1)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	mnt := res.MountPoint
	before := randomFile(t, filepath.Join(mnt, "before.bin"), 24<<20)
	l.cmd("sync")

	// Boot without disk 1: the normal mount fails, the pool says why.
	unit := storage.MountUnitName(name)
	l.cmd("systemctl", "stop", unit)
	l.unplug(1)
	l.cmd("btrfs", "device", "scan", "--forget")
	if r, _ := l.run.Run(ctx, "systemctl", "start", unit); r.ExitCode == 0 {
		t.Fatal("a mirror missing a disk must not mount without -o degraded")
	}
	st := l.status(name)
	if st.Mounted || st.Health != storage.HealthUnmounted || st.MissingDevices != 1 || !st.CanMountDegraded {
		t.Fatalf("unmounted status = %+v", st)
	}

	// Degraded mount: explicit, confirmed.
	if _, err := l.ops.poolMountDegraded(ctx, PoolConfirmArgs{Name: name, Confirm: "nope"}); errCode(err) != CodeConfirm {
		t.Fatalf("unconfirmed degraded mount: %v", err)
	}
	if _, err := l.ops.poolMountDegraded(ctx, PoolConfirmArgs{Name: name, Confirm: name}); err != nil {
		t.Fatalf("mount degraded: %v", err)
	}
	st = l.status(name)
	missing := 0
	for _, d := range st.Devices {
		if d.Missing {
			missing++
		}
	}
	if !st.Mounted || !st.MountedDegraded || st.MissingDevices != 1 || missing != 1 || st.Health != storage.HealthDegraded {
		t.Fatalf("degraded status = %+v", st)
	}
	sameContent(t, filepath.Join(mnt, "before.bin"), before)

	// Writes made now land in "single" chunks: no redundancy.
	during := randomFile(t, filepath.Join(mnt, "during.bin"), 16<<20)
	l.cmd("sync")
	if st = l.status(name); !st.MixedProfiles {
		t.Fatalf("writes while degraded should leave single chunks: %+v", st.Profiles)
	}
	if _, err := l.ops.poolRestoreRedundancy(ctx, PoolArgs{Name: name}); errCode(err) != CodeDiskMissing {
		t.Fatalf("restore with a disk missing: %v", err)
	}

	// Replacement: never with a disk in use, then with the blank one.
	if _, err := l.ops.poolReplace(ctx, PoolReplaceArgs{Name: name, Old: l.id(1), New: l.id(0), Confirm: name}); errCode(err) != CodeDiskNotAvailable {
		t.Fatalf("replace with a member disk: %v", err)
	}
	newID := l.id(2)
	rep, err := l.ops.poolReplace(ctx, PoolReplaceArgs{Name: name, Old: storage.LabIDPrefix + "loop999", New: newID, Confirm: name})
	if err != nil || rep.Method != "btrfs-replace" || rep.DevID != 2 {
		t.Fatalf("replace = %+v %v", rep, err)
	}
	st = l.waitStatus(name, "replace finished", func(s storage.PoolStatus) bool {
		return s.Replace != nil && s.Replace.State == "finished"
	})
	if st.MissingDevices != 0 || !st.MixedProfiles || st.Health != storage.HealthDegraded {
		t.Fatalf("after replace (single chunks remain) = %+v", st)
	}

	// Restore redundancy: convert the single chunks back to RAID1.
	out, err := l.ops.poolRestoreRedundancy(ctx, PoolArgs{Name: name})
	if err != nil || !out.BalanceStarted || out.Data != "raid1" || out.Metadata != "raid1" || out.Resized != 2 {
		t.Fatalf("restore = %+v %v", out, err)
	}
	st = l.waitStatus(name, "balance done", func(s storage.PoolStatus) bool { return s.Balance == nil && !s.MixedProfiles })
	if st.Health != storage.HealthOK {
		t.Fatalf("health after restore = %s (%+v)", st.Health, st)
	}
	sameContent(t, filepath.Join(mnt, "before.bin"), before)
	sameContent(t, filepath.Join(mnt, "during.bin"), during)
	if again, err := l.ops.poolRestoreRedundancy(ctx, PoolArgs{Name: name}); err != nil || again.BalanceStarted {
		t.Fatalf("second restore should have nothing to do: %+v %v", again, err)
	}

	// Next boot: a normal mount works again, and a scrub finds nothing.
	l.cmd("umount", mnt)
	l.cmd("systemctl", "start", unit)
	if st = l.status(name); !st.Mounted || st.MountedDegraded || st.Health != storage.HealthOK {
		t.Fatalf("after normal remount = %+v", st)
	}
	l.cmd("btrfs", "scrub", "start", "-B", mnt)
	if st = l.status(name); st.Scrub.Uncorrectable != 0 || st.Scrub.Corrected != 0 {
		t.Fatalf("scrub after replace = %+v", st.Scrub)
	}
	sameContent(t, filepath.Join(mnt, "during.bin"), during)
}

func TestLabMirrorCorruptionClearErrors(t *testing.T) {
	ctx := context.Background()
	l := newLabPool(t, 2)
	name, res, err := l.create(storage.LayoutMirror, 0, 1)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	data := randomFile(t, filepath.Join(res.MountPoint, "data.bin"), 200<<20)
	l.cmd("sync")

	// A disk returns garbage in its data area (bad sectors, flaky bridge):
	// overwrite part of disk 1's backing file behind Btrfs' back.
	img, ferr := os.OpenFile(filepath.Join(l.dir, "d1.img"), os.O_WRONLY, 0)
	if ferr != nil {
		t.Fatal(ferr)
	}
	junk := make([]byte, 64<<20)
	rand.Read(junk)
	img.WriteAt(junk, 300<<20)
	img.Close()
	l.cmd("sync")
	if err := os.WriteFile("/proc/sys/vm/drop_caches", []byte("3"), 0); err != nil {
		t.Fatal(err)
	}

	if _, err := l.ops.poolClearErrors(ctx, PoolArgs{Name: name}); errCode(err) != CodeScrubRequired {
		t.Fatalf("clear errors before any scrub: %v", err)
	}
	l.cmd("btrfs", "scrub", "start", "-B", res.MountPoint)
	st := l.status(name)
	if st.Health != storage.HealthErrors || st.Scrub.Corrected == 0 || st.Scrub.Uncorrectable != 0 {
		t.Fatalf("after scrub: health=%s scrub=%+v devices=%+v", st.Health, st.Scrub, st.Devices)
	}
	sameContent(t, filepath.Join(res.MountPoint, "data.bin"), data)

	// The scrub repaired everything: the counters may be reset.
	if _, err := l.ops.poolClearErrors(ctx, PoolArgs{Name: name}); err != nil {
		t.Fatalf("clear errors: %v", err)
	}
	if st = l.status(name); st.Health != storage.HealthOK {
		t.Fatalf("health after clear = %s %+v", st.Health, st.Devices)
	}
}

func TestLabParityFaultyMemberReplace(t *testing.T) {
	ctx := context.Background()
	l := newLabPool(t, 4)
	name, res, err := l.create(storage.LayoutParity1, 0, 1, 2)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	data := randomFile(t, filepath.Join(res.MountPoint, "data.bin"), 24<<20)
	l.cmd("sync")
	l.waitStatus(name, "initial resync", func(s storage.PoolStatus) bool { return s.MD != nil && s.MD.Sync == nil })

	failed := strings.TrimPrefix(l.loops[1], "/dev/") + "p1"
	l.cmd("mdadm", "--manage", storage.MDDevice(name), "--fail", "/dev/"+failed)
	st := l.status(name)
	if st.MD == nil || !slices.Equal(st.MD.Faulty, []string{failed}) || st.Health != storage.HealthDegraded {
		t.Fatalf("faulty status = %+v md=%+v", st, st.MD)
	}

	if _, err := l.ops.poolReplace(ctx, PoolReplaceArgs{Name: name, Old: l.id(0), New: l.id(3), Confirm: name}); errCode(err) != CodeMemberHealthy {
		t.Fatalf("replacing a healthy md member: %v", err)
	}
	rep, err := l.ops.poolReplace(ctx, PoolReplaceArgs{Name: name, Old: l.id(1), New: l.id(3), Confirm: name})
	if err != nil || rep.Method != "md-add" {
		t.Fatalf("replace = %+v %v", rep, err)
	}
	st = l.waitStatus(name, "md recovery", func(s storage.PoolStatus) bool {
		return s.MD != nil && s.MD.Sync == nil && !s.MD.Degraded()
	})
	newPart := strings.TrimPrefix(l.loops[3], "/dev/") + "p1"
	if st.Health != storage.HealthOK || !slices.Contains(st.MD.Members, newPart) || slices.Contains(st.MD.Members, failed) {
		t.Fatalf("after recovery = %+v md=%+v", st, st.MD)
	}
	sameContent(t, filepath.Join(res.MountPoint, "data.bin"), data)
}

func TestLabParityMissingAtBoot(t *testing.T) {
	ctx := context.Background()
	l := newLabPool(t, 3)
	name, res, err := l.create(storage.LayoutParity1, 0, 1, 2)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	data := randomFile(t, filepath.Join(res.MountPoint, "data.bin"), 8<<20)
	l.cmd("sync")
	l.waitStatus(name, "initial resync", func(s storage.PoolStatus) bool { return s.MD != nil && s.MD.Sync == nil })

	l.cmd("systemctl", "stop", storage.MountUnitName(name))
	l.cmd("mdadm", "--stop", storage.MDDevice(name))
	l.unplug(2)
	st := l.status(name)
	if st.Mounted || !st.CanMountDegraded {
		t.Fatalf("stopped array status = %+v", st)
	}
	if _, err := l.ops.poolMountDegraded(ctx, PoolConfirmArgs{Name: name, Confirm: name}); err != nil {
		t.Fatalf("mount degraded: %v", err)
	}
	st = l.status(name)
	if !st.Mounted || st.MD == nil || !st.MD.Degraded() || st.Health != storage.HealthDegraded {
		t.Fatalf("degraded parity = %+v md=%+v", st, st.MD)
	}
	sameContent(t, filepath.Join(res.MountPoint, "data.bin"), data)
	if _, err := l.ops.poolMountDegraded(ctx, PoolConfirmArgs{Name: name, Confirm: name}); errCode(err) != CodePoolMounted {
		t.Fatalf("second degraded mount: %v", err)
	}
}

// At boot, the initramfs assembles arrays with the mdadm.conf it was built
// with: a pool created since then comes up under its superblock's name
// (/dev/md/rpi5-nas:cwnsb-e2 on the Pi 5), without /dev/md/cwnsb-<pool>. The
// pool must still be seen and repaired; with a disk missing, its members are
// held in an inactive array of that other name, which the degraded mount
// must release first.
func TestLabParityNamedByTheInitramfs(t *testing.T) {
	ctx := context.Background()
	l := newLabPool(t, 3)
	name, res, err := l.create(storage.LayoutParity1, 0, 1, 2)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	l.waitStatus(name, "initial resync", func(s storage.PoolStatus) bool { return s.MD != nil && s.MD.Sync == nil })
	data := randomFile(t, filepath.Join(res.MountPoint, "data.bin"), 8<<20)
	l.cmd("sync")
	uuid := "--uuid=" + mdadmUUID(l.ops.mdUUID(name))
	parts := []string{l.loops[0] + "p1", l.loops[1] + "p1", l.loops[2] + "p1"}
	boot := "/dev/md/initramfs-" + name // not /dev/md/cwnsb-<pool>
	down := func() {
		l.cmd("systemctl", "stop", storage.MountUnitName(name))
		l.run.Run(ctx, "mdadm", "--stop", storage.MDDevice(name))
		l.run.Run(ctx, "mdadm", "--stop", boot)
		l.run.Run(ctx, "udevadm", "settle")
	}

	// 1. Every disk there: the array runs under the other name.
	down()
	l.cmd("mdadm", append([]string{"--assemble", boot, uuid, "--config=/dev/null"}, parts...)...)
	l.run.Run(ctx, "udevadm", "settle")
	l.cmd("systemctl", "start", storage.MountUnitName(name))
	if l.ops.Files.Exists(storage.MDDevice(name)) {
		t.Fatal("the pool's own name exists: nothing is tested")
	}
	st := l.status(name)
	if !st.Mounted || st.MD == nil || st.MD.Degraded() || st.MD.Active != 3 {
		t.Fatalf("array under another name not seen = %+v md=%+v", st, st.MD)
	}
	if _, err := l.ops.poolRepairParity(ctx, PoolArgs{Name: name}); err != nil {
		t.Fatalf("repair: %v", err)
	}
	l.waitStatus(name, "repaired", func(s storage.PoolStatus) bool { return s.MD != nil && s.MD.Sync == nil })

	// 2. A disk missing at boot: its two partners held, inactive, under the
	// other name. The degraded mount stops that array, then runs the pool's.
	down()
	for _, p := range parts[:2] { // what udev runs: not enough to start, held
		l.run.Run(ctx, "mdadm", "--incremental", "--config=/dev/null", p)
	}
	b, _ := os.ReadFile("/proc/mdstat")
	if !strings.Contains(string(b), "inactive") {
		t.Fatalf("no inactive array to release:\n%s", b)
	}
	l.unplug(2)
	st = l.status(name)
	if st.Mounted || !st.CanMountDegraded {
		t.Fatalf("held at boot = %+v r=%+v", st, st.Reassemble)
	}
	if _, err := l.ops.poolMountDegraded(ctx, PoolConfirmArgs{Name: name, Confirm: name}); err != nil {
		t.Fatalf("mount degraded: %v", err)
	}
	st = l.status(name)
	if !st.Mounted || st.MD == nil || !st.MD.Degraded() {
		t.Fatalf("degraded mount = %+v md=%+v", st, st.MD)
	}
	sameContent(t, filepath.Join(res.MountPoint, "data.bin"), data)
}

// A parity member unplugged and plugged back in (ADR-0027): udev removes it
// ("mdadm -If") but does not put it back. "Take it back" re-adds it from the
// bitmap, without unmounting the pool.
func TestLabParityMemberTakenBack(t *testing.T) {
	ctx := context.Background()
	l := newLabPool(t, 3)
	name, res, err := l.create(storage.LayoutParity1, 0, 1, 2)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	before := randomFile(t, filepath.Join(res.MountPoint, "before.bin"), 24<<20)
	l.cmd("sync")
	l.waitStatus(name, "initial resync", func(s storage.PoolStatus) bool { return s.MD != nil && s.MD.Sync == nil })

	part := strings.TrimPrefix(l.loops[1], "/dev/") + "p1"
	l.cmd("mdadm", "--manage", storage.MDDevice(name), "--fail", "/dev/"+part)
	l.cmd("mdadm", "-If", part) // what udev runs when the disk leaves
	during := randomFile(t, filepath.Join(res.MountPoint, "during.bin"), 16<<20)
	l.cmd("sync")

	st := l.status(name)
	if !st.CanResume || st.MissingDevices != 1 || st.Health != storage.HealthDegraded {
		t.Fatalf("member back, out of the array = %+v md=%+v", st, st.MD)
	}
	out, err := l.ops.poolResume(ctx, PoolResumeArgs{Name: name, Confirm: name})
	if err != nil || out.Remounted || !out.ResyncStarted {
		t.Fatalf("resume = %+v %v", out, err)
	}
	st = l.waitStatus(name, "md complete", func(s storage.PoolStatus) bool {
		return s.MD != nil && s.MD.Sync == nil && !s.MD.Degraded()
	})
	if !slices.Contains(st.MD.Members, part) || st.CanResume || !st.Mounted {
		t.Fatalf("after resume = %+v md=%+v", st, st.MD)
	}
	sameContent(t, filepath.Join(res.MountPoint, "before.bin"), before)
	sameContent(t, filepath.Join(res.MountPoint, "during.bin"), during)
}
