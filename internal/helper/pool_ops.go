// SPDX-License-Identifier: AGPL-3.0-or-later

package helper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/crosswax/nasbuster/internal/apps"
	"github.com/crosswax/nasbuster/internal/storage"
	"github.com/crosswax/nasbuster/internal/validate"
)

var fsUUIDRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// poolCreate formats blank disks into a new pool (ADR-0008).
//
// Safety: the helper never trusts the caller about disks. It runs its own
// discovery and only touches disks it sees as "available" (no partition
// table, filesystem, RAID/LVM signature or mount, stable id). On failure it
// undoes what it did, which can only concern those same, previously blank disks.
func (o *Ops) poolCreate(ctx context.Context, a PoolCreateArgs) (PoolCreateResult, *Error) {
	layout, disks, opErr := o.checkPoolCreate(ctx, a)
	if opErr != nil {
		return PoolCreateResult{}, opErr
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	x := &poolBuild{o: o, ctx: ctx, name: a.Name}
	result, err := x.run(layout, disks)
	if err != nil {
		o.logger().Error("pool creation failed, rolling back", "pool", a.Name, "err", err.Message)
		x.rollback()
		return PoolCreateResult{}, err
	}
	return result, nil
}

// checkPoolCreate validates everything before the first write.
func (o *Ops) checkPoolCreate(ctx context.Context, a PoolCreateArgs) (storage.Layout, []storage.Disk, *Error) {
	if !storage.ValidPoolName(a.Name) {
		return storage.Layout{}, nil, errorf(CodePoolName, "invalid pool name")
	}
	layout, ok := storage.LayoutByName(a.Layout)
	if !ok {
		return layout, nil, errorf(CodeLayout, "unknown layout")
	}
	if a.Confirm != a.Name {
		return layout, nil, errorf(CodeConfirm, "confirmation does not match the pool name")
	}
	if len(a.Disks) < layout.MinDisks || (layout.MaxDisks > 0 && len(a.Disks) > layout.MaxDisks) || len(a.Disks) > 16 {
		return layout, nil, errorf(CodeDiskCount, "%s needs at least %d disks", layout.Name, layout.MinDisks)
	}
	seen := map[string]bool{}
	for _, id := range a.Disks {
		if !validate.DiskID(id) || seen[id] {
			return layout, nil, errorf(CodeInvalidArgs, "invalid or repeated disk id")
		}
		seen[id] = true
	}

	// The helper's own view of the disks: never the caller's. Let udev finish
	// pending events first so the view is as fresh as it can be.
	_ = o.check(ctx, "udevadm", "settle", "--timeout=10")
	all, err := o.Discover(ctx)
	if err != nil {
		return layout, nil, errorf(CodeFailed, "discover disks: %v", err)
	}
	byID := map[string]storage.Disk{}
	for _, d := range all {
		byID[d.ID] = d
	}
	disks := make([]storage.Disk, 0, len(a.Disks))
	sizes := make([]uint64, 0, len(a.Disks))
	for _, id := range a.Disks {
		d, ok := byID[id]
		if !ok {
			return layout, nil, errorf(CodeDiskNotFound, "no such disk: %s", id)
		}
		if d.Role != storage.RoleAvailable {
			return layout, nil, errorf(CodeDiskNotAvailable, "disk %s is %s, only blank disks can be used", id, d.Role).
				with(map[string]any{"disk": id, "role": d.Role})
		}
		if d.Transport != "loop" && d.ByIDPath == "" {
			return layout, nil, errorf(CodeDiskNotAvailable, "disk %s has no stable path", id)
		}
		disks = append(disks, d)
		sizes = append(sizes, d.SizeBytes)
	}
	if _, err := storage.PlanPool(layout.Name, sizes); err != nil {
		if errors.Is(err, storage.ErrDiskTooSmall) {
			return layout, nil, errorf(CodeDiskTooSmall, "a disk is smaller than %d MiB", storage.MinDiskBytes>>20)
		}
		return layout, nil, errorf(CodeDiskCount, "%v", err)
	}

	unit := path.Join(o.UnitDir, storage.MountUnitName(a.Name))
	if o.Files.Exists(storage.MountPoint(a.Name)) || o.Files.Exists(unit) || o.Files.Exists(storage.MDDevice(a.Name)) {
		return layout, nil, errorf(CodePoolExists, "a pool named %s already exists", a.Name)
	}
	// Last line of defence, right before the first write.
	for _, d := range disks {
		if err := o.verifyBlank(ctx, d); err != nil {
			return layout, nil, err
		}
	}
	return layout, disks, nil
}

// verifyBlank re-checks a disk at the lowest level. Discovery relies on lsblk,
// which reads the udev database, and that database can lag behind a recent
// write (found in the lab: a disk freshly formatted ext4 still looked blank).
// So before any destructive step the helper probes the device itself with
// blkid -p and reads the kernel's view of partitions and holders in sysfs.
// Anything it cannot verify is refused.
func (o *Ops) verifyBlank(ctx context.Context, d storage.Disk) *Error {
	found, perr := o.probeSignature(ctx, devPath(d))
	if perr != nil {
		return perr
	}
	if found != "" { // several signatures (ambiguous) included: even more reason to refuse
		return errorf(CodeDiskNotAvailable, "disk %s contains %s, only blank disks can be used", d.ID, found).
			with(map[string]any{"disk": d.ID, "role": storage.RoleInUse, "found": found})
	}

	kname := path.Base(d.DevPath)
	entries, err := fs.ReadDir(o.FS, "sys/block/"+kname)
	if err != nil {
		return errorf(CodeFailed, "inspect %s in sysfs: %v", d.ID, err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), kname) { // sda1, loop2p1, nvme0n1p1
			return errorf(CodeDiskNotAvailable, "disk %s has a partition (%s)", d.ID, e.Name()).
				with(map[string]any{"disk": d.ID, "role": storage.RoleInUse, "found": "partition"})
		}
	}
	if holders, err := fs.ReadDir(o.FS, "sys/block/"+kname+"/holders"); err == nil && len(holders) > 0 {
		return errorf(CodeDiskNotAvailable, "disk %s is held by %s", d.ID, holders[0].Name()).
			with(map[string]any{"disk": d.ID, "role": storage.RoleInUse, "found": "holder"})
	}
	return nil
}

// probeSignature reads a device directly (blkid -p, never the udev cache) and
// names what it holds: "" for nothing, a file system or partition table type,
// "unknown" for a signature blkid cannot name.
// A probe that cannot answer is an error: no write on a doubt.
func (o *Ops) probeSignature(ctx context.Context, dev string) (string, *Error) {
	res, err := o.Run.Run(ctx, "blkid", "-p", "-o", "export", dev)
	if err != nil {
		return "", errorf(CodeFailed, "probe %s: %v", dev, err)
	}
	switch res.ExitCode {
	case 2: // no signature at all
		return "", nil
	case 0, 8: // 8 = several signatures (ambiguous)
		props := storage.ParseMDExport(string(res.Stdout)) // same KEY=VALUE format
		if found := firstNonEmptyStr(props["TYPE"], props["PTTYPE"]); found != "" {
			return found, nil
		}
		// On a partition, blkid always describes its entry in the partition
		// table (PART_ENTRY_*, exit 0): that is not content. Anything else is.
		for k := range props {
			if k != "DEVNAME" && !strings.HasPrefix(k, "PART_ENTRY_") {
				return "unknown", nil
			}
		}
		if res.ExitCode == 8 {
			return "unknown", nil
		}
		return "", nil
	default:
		return "", errorf(CodeFailed, "probe %s: blkid exited with %d", dev, res.ExitCode)
	}
}

func firstNonEmptyStr(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// poolBuild runs the creation steps and remembers what to undo.
type poolBuild struct {
	o    *Ops
	ctx  context.Context
	name string

	partitioned []storage.Disk // disks that received our partition table
	formatting  bool           // mdadm or mkfs may have written inside the partitions
	mdCreated   bool
	mdConfBlock bool
	mountDir    bool
	unitWritten bool
	unitEnabled bool
}

func devPath(d storage.Disk) string {
	if d.Transport == "loop" {
		return d.DevPath // lab only: loops have no by-id link
	}
	return d.ByIDPath
}

func partPath(d storage.Disk) string {
	if d.Transport == "loop" {
		return d.DevPath + "p1"
	}
	return d.ByIDPath + "-part1"
}

func (x *poolBuild) run(layout storage.Layout, disks []storage.Disk) (PoolCreateResult, *Error) {
	o, name := x.o, x.name
	label := storage.FSLabel(name)

	// 1. One GPT partition per disk, leaving 100 MiB free at the end.
	for _, d := range disks {
		if err := o.check(x.ctx, "sgdisk", "-n", "1:0:-100M", "-t", "1:"+layout.PartitionType(), "-c", "1:"+label, devPath(d)); err != nil {
			return PoolCreateResult{}, err
		}
		x.partitioned = append(x.partitioned, d)
	}
	_ = o.check(x.ctx, "udevadm", "settle")
	parts := make([]string, 0, len(disks))
	for _, d := range disks {
		p := partPath(d)
		if !x.waitFor(p) {
			return PoolCreateResult{}, errorf(CodeFailed, "partition %s did not appear", p)
		}
		parts = append(parts, p)
	}

	// A disk wiped by another tool (only its partition table removed) still
	// holds its old file systems: the disk-level probe cannot see them, but the
	// new partition may start exactly where an old one did. Refuse rather than
	// overwrite: it may be a file system someone wants back (no -f below).
	for i, p := range parts {
		found, err := o.probeSignature(x.ctx, p)
		if err != nil {
			return PoolCreateResult{}, err
		}
		if found != "" {
			return PoolCreateResult{}, errorf(CodeDiskResidual, "disk %s still holds old data (%s) where the new partition starts", disks[i].ID, found).
				with(map[string]any{"disk": disks[i].ID, "found": found})
		}
	}

	// From here on, NasBuster writes inside the new partitions: a rollback
	// must wipe what it wrote (and only then).
	x.formatting = true

	// 2. Parity layouts: an md array with a write-intent bitmap (fast re-add
	//    after a USB disconnect), then a single-device Btrfs on top.
	targets := parts
	md := ""
	if layout.MDLevel > 0 {
		md = storage.MDDevice(name)
		args := append([]string{"--create", md, "--run", fmt.Sprintf("--level=%d", layout.MDLevel),
			fmt.Sprintf("--raid-devices=%d", len(parts)), "--metadata=1.2", "--bitmap=internal",
			"--name=" + label}, parts...)
		if err := o.check(x.ctx, "mdadm", args...); err != nil {
			return PoolCreateResult{}, err
		}
		x.mdCreated = true
		_ = o.check(x.ctx, "udevadm", "settle")
		if !x.waitFor(md) {
			return PoolCreateResult{}, errorf(CodeFailed, "array %s did not appear", md)
		}
		targets = []string{md}
	}

	// 3. Btrfs. No -f: if a stale signature hides in the new partition, mkfs
	//    refuses and we stop rather than overwrite something unexpected.
	data, meta := layout.BtrfsProfiles(len(targets))
	if err := o.check(x.ctx, "mkfs.btrfs", append([]string{"-L", label, "-d", data, "-m", meta}, targets...)...); err != nil {
		return PoolCreateResult{}, err
	}
	_ = o.check(x.ctx, "udevadm", "settle")
	// -p probes the device itself: the blkid cache may predate mkfs.
	res, err := o.Run.Run(x.ctx, "blkid", "-p", "-s", "UUID", "-o", "value", targets[0])
	uuid := strings.TrimSpace(string(res.Stdout))
	if err != nil || !fsUUIDRe.MatchString(uuid) {
		return PoolCreateResult{}, errorf(CodeFailed, "read filesystem uuid: %q %v", uuid, err)
	}

	// 4. Persist the md array definition.
	if md != "" {
		res, err := o.Run.Run(x.ctx, "mdadm", "--detail", "--brief", md)
		line := strings.TrimSpace(string(res.Stdout))
		if err != nil || !strings.HasPrefix(line, "ARRAY ") {
			return PoolCreateResult{}, errorf(CodeFailed, "mdadm --detail --brief: %v", err)
		}
		if err := x.addMDConf(line); err != nil {
			return PoolCreateResult{}, err
		}
	}

	// 5. Mount through a generated systemd unit.
	mnt := storage.MountPoint(name)
	if err := o.Files.MkdirAll(mnt, 0o755); err != nil {
		return PoolCreateResult{}, errorf(CodeFailed, "create %s: %v", mnt, err)
	}
	x.mountDir = true
	unitName := storage.MountUnitName(name)
	if err := o.Files.WriteFile(path.Join(o.UnitDir, unitName), []byte(storage.MountUnit(name, uuid)), 0o644); err != nil {
		return PoolCreateResult{}, errorf(CodeFailed, "write unit: %v", err)
	}
	x.unitWritten = true
	if err := o.check(x.ctx, "systemctl", "daemon-reload"); err != nil {
		return PoolCreateResult{}, err
	}
	if err := o.check(x.ctx, "systemctl", "enable", "--now", unitName); err != nil {
		return PoolCreateResult{}, err
	}
	x.unitEnabled = true

	// 6. Verify the result as the system sees it.
	mounted, fsType, gotUUID := o.findmnt(x.ctx, mnt)
	if !mounted || fsType != "btrfs" || gotUUID != uuid {
		return PoolCreateResult{}, errorf(CodeFailed, "pool not mounted as expected (%v %s %s)", mounted, fsType, gotUUID)
	}

	members := make([]string, 0, len(disks))
	for _, d := range disks {
		members = append(members, d.ID)
	}
	return PoolCreateResult{Name: name, Layout: layout.Name, UUID: uuid, MDDevice: md, Members: members, MountPoint: mnt}, nil
}

func (x *poolBuild) waitFor(p string) bool {
	for i := 0; i < 40; i++ {
		if x.o.Files.Exists(p) {
			return true
		}
		x.o.sleep(250 * time.Millisecond)
	}
	return false
}

// The markers around every block NasBuster writes in mdadm.conf.
const (
	mdConfMarkerPrefix = "# BEGIN CrossWax NasBuster pool "
	mdConfEndPrefix    = "# END CrossWax NasBuster pool "
)

func mdConfMarkers(name string) (begin, end string) {
	return mdConfMarkerPrefix + name, mdConfEndPrefix + name
}

func (x *poolBuild) addMDConf(arrayLine string) *Error {
	x.o.mdConfMu.Lock()
	defer x.o.mdConfMu.Unlock()
	begin, end := mdConfMarkers(x.name)
	current, err := x.o.Files.ReadFile(x.o.MDConf)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return errorf(CodeFailed, "read %s: %v", x.o.MDConf, err)
	}
	content := strings.TrimRight(string(current), "\n")
	if content != "" {
		content += "\n"
	}
	content += begin + "\n" + withoutSpares(arrayLine) + "\n" + end + "\n"
	content, disabled := foreignDuplicatesDisabled(content)
	if err := x.o.Files.WriteFile(x.o.MDConf, []byte(content), 0o644); err != nil {
		return errorf(CodeFailed, "write %s: %v", x.o.MDConf, err)
	}
	x.mdConfBlock = true
	if len(disabled) > 0 {
		x.o.logger().Info("mdadm.conf: another ARRAY line for this array commented out", "pool", x.name)
	}
	return nil
}

// withoutSpares drops "spares=N" from an ARRAY line: a new RAID5 builds its
// parity on a member counted as a spare (mdadm --detail --brief says
// "spares=1", seen on the Pi 5), and mdadm --monitor would then report the
// missing spare forever.
func withoutSpares(arrayLine string) string {
	fields := strings.Fields(arrayLine)
	return strings.Join(slices.DeleteFunc(fields, func(f string) bool { return strings.HasPrefix(f, "spares=") }), " ")
}

// mdConfDisabledPrefix starts a line of the administrator's that NasBuster
// commented out, followed by the pool's name, ": " and the original line.
const mdConfDisabledPrefix = "# Disabled by CrossWax NasBuster, duplicate of pool "

// foreignDuplicatesDisabled comments out every ARRAY line outside NasBuster's
// blocks that names the array of one of them, by UUID or by device name.
// Debian's mdadm package writes a line for every array it finds when it is
// installed: on a fresh system (a dead SD card, ADR-0014), an imported parity
// pool then has two lines, and mdadm assembles nothing at the next boot,
// neither "--assemble --scan" in the initramfs nor "--incremental" from udev
// (checked in the lab with mdadm 4.4). The line stays in the file, marked
// with the pool's name, so that a failed operation can put it back. It
// returns the pools that had such a line.
func foreignDuplicatesDisabled(conf string) (string, []string) {
	type ident struct{ pool, device, uuid string }
	var ours []ident
	lines := strings.Split(conf, "\n")
	pool := ""
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, mdConfMarkerPrefix):
			pool = strings.TrimPrefix(line, mdConfMarkerPrefix)
		case strings.HasPrefix(line, mdConfEndPrefix):
			pool = ""
		case pool != "" && isArrayLine(line):
			id := ident{pool: pool, device: strings.Fields(line)[1]}
			if m := mdConfUUIDRe.FindStringSubmatch(line); m != nil {
				id.uuid = hexOnly(m[1])
			}
			ours = append(ours, id)
		}
	}
	var disabled []string
	inBlock := false
	for i := 0; i < len(lines); i++ {
		switch {
		case strings.HasPrefix(lines[i], mdConfMarkerPrefix):
			inBlock = true
		case strings.HasPrefix(lines[i], mdConfEndPrefix):
			inBlock = false
		case !inBlock && isArrayLine(lines[i]):
			// An ARRAY entry goes on over the lines that start with blanks.
			end := i + 1
			for end < len(lines) && lines[end] != "" && (lines[end][0] == ' ' || lines[end][0] == '\t') {
				end++
			}
			entry := strings.Join(lines[i:end], " ")
			uuid := ""
			if m := mdConfUUIDRe.FindStringSubmatch(entry); m != nil {
				uuid = hexOnly(m[1])
			}
			for _, id := range ours {
				if (uuid != "" && uuid == id.uuid) || strings.Fields(entry)[1] == id.device {
					for j := i; j < end; j++ {
						lines[j] = mdConfDisabledPrefix + id.pool + ": " + lines[j]
					}
					if !slices.Contains(disabled, id.pool) {
						disabled = append(disabled, id.pool)
					}
					break
				}
			}
			i = end - 1
		}
	}
	return strings.Join(lines, "\n"), disabled
}

// isArrayLine reports whether a mdadm.conf line starts an ARRAY entry: mdadm
// takes its keywords in any case, and shortened to three letters or more.
func isArrayLine(line string) bool {
	f := strings.Fields(line)
	return len(f) >= 2 && line[0] != ' ' && line[0] != '\t' && len(f[0]) >= 3 && strings.HasPrefix("ARRAY", strings.ToUpper(f[0]))
}

// foreignDuplicatesRestored puts back the lines commented out for a pool.
func foreignDuplicatesRestored(conf, name string) string {
	prefix := mdConfDisabledPrefix + name + ": "
	lines := strings.Split(conf, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimPrefix(line, prefix)
	}
	return strings.Join(lines, "\n")
}

// rollback undoes a failed creation, best effort, in reverse order. Every
// destructive step targets only the disks validated as blank by this call.
func (x *poolBuild) rollback() {
	o, ctx := x.o, context.Background()
	unitName := storage.MountUnitName(x.name)
	if x.unitEnabled {
		o.try(ctx, "systemctl", "disable", "--now", unitName)
	}
	if x.unitWritten {
		_ = o.Files.Remove(path.Join(o.UnitDir, unitName))
		o.try(ctx, "systemctl", "daemon-reload")
	}
	if x.mountDir {
		_ = o.Files.Remove(storage.MountPoint(x.name)) // only if empty
	}
	if x.mdConfBlock {
		x.removeMDConf()
		x.restoreForeignLines()
	}
	if x.mdCreated {
		o.try(ctx, "mdadm", "--stop", storage.MDDevice(x.name))
	}
	for _, d := range x.partitioned {
		// Only what NasBuster wrote is wiped: a residual signature found before
		// formatting belongs to someone else and stays.
		if x.formatting {
			o.try(ctx, "wipefs", "-a", partPath(d))
		}
		o.try(ctx, "sgdisk", "--zap-all", devPath(d))
	}
	o.try(ctx, "udevadm", "settle")
}

func (x *poolBuild) removeMDConf() {
	x.o.mdConfMu.Lock()
	defer x.o.mdConfMu.Unlock()
	begin, end := mdConfMarkers(x.name)
	current, err := x.o.Files.ReadFile(x.o.MDConf)
	if err != nil {
		return
	}
	var kept []string
	skip := false
	for _, line := range strings.Split(string(current), "\n") {
		switch {
		case line == begin:
			skip = true
		case line == end:
			skip = false
		case !skip:
			kept = append(kept, line)
		}
	}
	_ = x.o.Files.WriteFile(x.o.MDConf, []byte(strings.Join(kept, "\n")), 0o644)
}

// restoreForeignLines puts back the administrator's lines that addMDConf
// commented out, when the operation that wrote the block fails.
func (x *poolBuild) restoreForeignLines() {
	x.o.mdConfMu.Lock()
	defer x.o.mdConfMu.Unlock()
	current, err := x.o.Files.ReadFile(x.o.MDConf)
	if err != nil {
		return
	}
	if restored := foreignDuplicatesRestored(string(current), x.name); restored != string(current) {
		_ = x.o.Files.WriteFile(x.o.MDConf, []byte(restored), 0o644)
	}
}

// try runs a rollback command and only logs failures.
func (o *Ops) try(ctx context.Context, name string, args ...string) {
	if err := o.check(ctx, name, args...); err != nil {
		o.logger().Warn("rollback step failed", "cmd", name, "args", args, "err", err.Message)
	}
}

// findmnt reports whether mnt is a mount point, its type and filesystem UUID.
func (o *Ops) findmnt(ctx context.Context, mnt string) (mounted bool, fsType, uuid string) {
	res, err := o.Run.Run(ctx, "findmnt", "-J", "-o", "TARGET,SOURCE,FSTYPE,UUID", "--mountpoint", mnt)
	if err != nil || res.ExitCode != 0 {
		return false, "", ""
	}
	var out struct {
		Filesystems []struct {
			Target string `json:"target"`
			FSType string `json:"fstype"`
			UUID   string `json:"uuid"`
		} `json:"filesystems"`
	}
	if json.Unmarshal(res.Stdout, &out) != nil || len(out.Filesystems) == 0 || out.Filesystems[0].Target != mnt {
		return false, "", ""
	}
	return true, out.Filesystems[0].FSType, out.Filesystems[0].UUID
}

// poolStatus reads the live state of a pool (all read-only commands).
func (o *Ops) poolStatus(ctx context.Context, a PoolArgs) (storage.PoolStatus, *Error) {
	if !storage.ValidPoolName(a.Name) {
		return storage.PoolStatus{}, errorf(CodePoolName, "invalid pool name")
	}
	mnt := storage.MountPoint(a.Name)
	st := storage.PoolStatus{Name: a.Name, MountPoint: mnt, Devices: []storage.PoolDevice{}, Profiles: []storage.ProfileUsage{},
		Scrub: storage.ScrubStatus{Status: "never"}}
	m := o.mountInfo(ctx, mnt)
	st.Mounted, st.UUID = m.Mounted, m.UUID
	if st.Mounted && m.FSType == "btrfs" {
		if res, err := o.Run.Run(ctx, "btrfs", "--format", "json", "device", "stats", mnt); err == nil {
			if devs, err := storage.ParseDeviceStats(res.Stdout); err == nil {
				st.Devices = devs
			}
		}
		if res, err := o.Run.Run(ctx, "btrfs", "--format", "json", "filesystem", "df", mnt); err == nil {
			if df, err := storage.ParseFilesystemDF(res.Stdout); err == nil {
				st.Profiles = df
			}
		}
		if res, err := o.Run.Run(ctx, "btrfs", "filesystem", "usage", "-b", mnt); err == nil {
			st.SizeBytes, st.UsedBytes, st.FreeBytes, st.Missing = storage.ParseFilesystemUsage(string(res.Stdout))
			st.Slack = storage.ParseDeviceSlack(string(res.Stdout))
		}
		st.Scrub = o.scrubStatus(ctx, mnt)
	}
	if md := o.mdStatus(a.Name); md != nil {
		st.MD = md
	}
	o.failureState(ctx, a.Name, m, &st)
	st.ComputeUsage()
	st.ComputeHealth()
	return st, nil
}

// mdStatus resolves /dev/md/cwnsb-<name> to its kernel name and reads /proc/mdstat.
func (o *Ops) mdStatus(name string) *storage.MDStatus {
	kernel := o.mdKernel(name)
	if kernel == "" {
		return nil
	}
	b, err := fs.ReadFile(o.FS, "proc/mdstat")
	if err != nil {
		return nil
	}
	md, ok := storage.ParseMDStat(string(b))[kernel]
	if !ok {
		return nil
	}
	md.Device = storage.MDDevice(name)
	// /proc/mdstat calls a parity repair (ADR-0032) "resync" too, and shows a
	// requested check or repair only once it has begun: sysfs tells them. Not
	// for a rebuild: right after a member fails, with no spare to rebuild on,
	// sysfs says "recover" for a moment while nothing runs (lab, 5.15).
	action := o.mdSysfs(kernel, "sync_action")
	switch {
	case md.Sync != nil && md.Sync.Action == "resync" && action == "repair":
		md.Sync.Action = "repair"
	case md.Sync == nil && (action == "repair" || action == "check"):
		md.Sync = &storage.MDSync{Action: action}
	case md.Sync == nil && action == "idle":
		at := o.mdSysfs(kernel, "sync_min")
		md.Interrupted = at != "" && at != "0"
	}
	if md.Members == nil {
		md.Members = []string{}
	}
	if md.Faulty == nil {
		md.Faulty = []string{}
	}
	return &md
}

// mdSysfs reads one attribute of an md array (/sys/block/<kernel>/md/<attr>),
// "" when unreadable.
func (o *Ops) mdSysfs(kernel, attr string) string {
	b, err := fs.ReadFile(o.FS, "sys/block/"+kernel+"/md/"+attr)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// scrubStatus reads the last scrub of a mounted pool ("never" if unknown).
func (o *Ops) scrubStatus(ctx context.Context, mnt string) storage.ScrubStatus {
	if res, err := o.Run.Run(ctx, "btrfs", "scrub", "status", "-R", mnt); err == nil && res.ExitCode == 0 {
		return storage.ParseScrubStatus(string(res.Stdout))
	}
	return storage.ScrubStatus{Status: "never"}
}

// poolScrub starts a Btrfs scrub (self-heals mirrors) in its own unit: it can
// take hours, longer than the helper lives (ADR-0026).
func (o *Ops) poolScrub(ctx context.Context, a PoolArgs) (map[string]bool, *Error) {
	if !storage.ValidPoolName(a.Name) {
		return nil, errorf(CodePoolName, "invalid pool name")
	}
	mnt := storage.MountPoint(a.Name)
	if mounted, fsType, _ := o.findmnt(ctx, mnt); !mounted || fsType != "btrfs" {
		return nil, errorf(CodePoolNotMounted, "pool %s is not mounted", a.Name)
	}
	if o.scrubStatus(ctx, mnt).Status == "running" || o.resyncRunning(ctx, a.Name) {
		return nil, errorf(CodePoolBusy, "a scrub is already running")
	}
	cmd := []string{btrfsBin, "scrub", "start", "-B", mnt}
	if err := o.startPoolJob(ctx, apps.PoolJob(apps.ScrubJob, a.Name), nil, cmd); err != nil {
		return nil, err
	}
	return map[string]bool{"started": true}, nil
}

// poolScrubCancel stops a running scrub. btrfs keeps its progress: the status
// becomes "aborted", and poolScrubResume goes on from there.
func (o *Ops) poolScrubCancel(ctx context.Context, a PoolArgs) (map[string]bool, *Error) {
	if !storage.ValidPoolName(a.Name) {
		return nil, errorf(CodePoolName, "invalid pool name")
	}
	mnt := storage.MountPoint(a.Name)
	if mounted, fsType, _ := o.findmnt(ctx, mnt); !mounted || fsType != "btrfs" {
		return nil, errorf(CodePoolNotMounted, "pool %s is not mounted", a.Name)
	}
	if o.scrubStatus(ctx, mnt).Status != "running" {
		return nil, errorf(CodeScrubNotRunning, "no scrub is running on pool %s", a.Name)
	}
	// The cancel returns once the scrub has stopped: on a slow (or throttled)
	// disk, longer than the default 30 s (seen in the lab).
	if err := o.checkTimeout(ctx, 2*time.Minute, "btrfs", "scrub", "cancel", mnt); err != nil {
		return nil, err
	}
	// The unit ends once "scrub start -B" has returned: wait for it, so that a
	// resume right after is not refused as "already running".
	job := apps.PoolJob(apps.ScrubJob, a.Name)
	for i := 0; i < 20 && o.job(ctx, job, 0).State == "running"; i++ {
		o.sleep(250 * time.Millisecond)
	}
	return map[string]bool{"canceled": true}, nil
}

// poolScrubResume goes on with a scrub that was stopped ("aborted") or cut by
// a reboot ("interrupted"), instead of reading the whole pool again: hours on
// a big disk. Same unit as a scrub (ADR-0026).
func (o *Ops) poolScrubResume(ctx context.Context, a PoolArgs) (map[string]bool, *Error) {
	if !storage.ValidPoolName(a.Name) {
		return nil, errorf(CodePoolName, "invalid pool name")
	}
	mnt := storage.MountPoint(a.Name)
	if mounted, fsType, _ := o.findmnt(ctx, mnt); !mounted || fsType != "btrfs" {
		return nil, errorf(CodePoolNotMounted, "pool %s is not mounted", a.Name)
	}
	if o.resyncRunning(ctx, a.Name) {
		return nil, errorf(CodePoolBusy, "the pool is being resynchronized")
	}
	switch o.scrubStatus(ctx, mnt).Status {
	case "aborted", "interrupted":
	case "running":
		return nil, errorf(CodePoolBusy, "a scrub is already running")
	default:
		return nil, errorf(CodeScrubNotStopped, "no stopped scrub to resume on pool %s", a.Name)
	}
	cmd := []string{btrfsBin, "scrub", "resume", "-B", mnt}
	if err := o.startPoolJob(ctx, apps.PoolJob(apps.ScrubJob, a.Name), nil, cmd); err != nil {
		return nil, err
	}
	return map[string]bool{"resumed": true}, nil
}

func (o *Ops) sleep(d time.Duration) {
	if o.Sleep != nil {
		o.Sleep(d)
		return
	}
	time.Sleep(d)
}
