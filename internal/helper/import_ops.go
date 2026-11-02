// SPDX-License-Identifier: AGPL-3.0-or-later

package helper

import (
	"context"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/crosswax/nasbuster/internal/storage"
)

// Import of existing CWNSB pools (ADR-0014): after the system was reinstalled
// (dead SD card) or when disks come from another CWNSB NAS. Members are
// recognised by their GPT partition name "cwnsb-<pool>", which pool creation
// writes for both layouts (the md label carries the old host name).

// importGroup gathers the present members of one pool.
type importGroup struct {
	name  string
	kind  string // btrfs, md
	uuid  string // filesystem UUID (btrfs) or array UUID (md)
	parts []string
	disks []storage.Disk
}

func (o *Ops) importGroups(ctx context.Context) (map[string]*importGroup, *Error) {
	_ = o.check(ctx, "udevadm", "settle", "--timeout=10")
	all, err := o.Discover(ctx)
	if err != nil {
		return nil, errorf(CodeFailed, "discover disks: %v", err)
	}
	groups := map[string]*importGroup{}
	for _, d := range all {
		if d.Role == storage.RoleSystem || d.Role == storage.RoleProtected {
			continue
		}
		for _, p := range d.Partitions {
			name, ok := strings.CutPrefix(p.PartLabel, "cwnsb-")
			if !ok || !storage.ValidPoolName(name) || o.Files.Exists(path.Join(o.UnitDir, storage.MountUnitName(name))) {
				continue // not ours, or a pool this system already knows
			}
			kind := ""
			switch p.FSType {
			case "btrfs":
				kind = "btrfs"
			case "linux_raid_member":
				kind = "md"
			default:
				continue // partitioned but never formatted: nothing to import
			}
			g := groups[name]
			if g == nil {
				g = &importGroup{name: name, kind: kind, uuid: p.UUID}
				groups[name] = g
			}
			if g.kind != kind || g.uuid != p.UUID {
				continue // a stray disk from another pool of the same name
			}
			g.parts = append(g.parts, p.Name)
			g.disks = append(g.disks, d)
		}
	}
	return groups, nil
}

// poolsScan lists the CWNSB pools found on the disks that this system does not know.
func (o *Ops) poolsScan(ctx context.Context) (ImportScan, *Error) {
	groups, err := o.importGroups(ctx)
	if err != nil {
		return ImportScan{}, err
	}
	out := ImportScan{Pools: []ImportCandidate{}}
	for _, g := range groups {
		c := ImportCandidate{Name: g.name, Kind: g.kind, Present: len(g.parts), Disks: []string{}}
		for _, d := range g.disks {
			c.Disks = append(c.Disks, d.ID)
		}
		switch g.kind {
		case "btrfs":
			if show, ok := o.btrfsShow(ctx, g.uuid); ok {
				c.Expected = show.Total
			}
			c.Layout = storage.LayoutMirror // refined at import from the profiles
			if c.Expected == 1 {
				c.Layout = storage.LayoutSingle
			}
		case "md":
			res, err := o.Run.Run(ctx, "mdadm", "--examine", "--export", "/dev/"+g.parts[0])
			if err == nil && res.ExitCode == 0 {
				ex := storage.ParseMDExport(string(res.Stdout))
				c.Expected, _ = strconv.Atoi(ex["MD_DEVICES"])
				c.Layout = map[string]string{"raid5": storage.LayoutParity1, "raid6": storage.LayoutParity2}[ex["MD_LEVEL"]]
			}
		}
		c.Complete = c.Expected > 0 && c.Present >= c.Expected
		out.Pools = append(out.Pools, c)
	}
	sort.Slice(out.Pools, func(i, j int) bool { return out.Pools[i].Name < out.Pools[j].Name })
	return out, nil
}

// poolImport adopts a complete pool found by poolsScan: assemble (md),
// mount through a generated unit, as pool creation does, without writing
// anything to the disks themselves.
func (o *Ops) poolImport(ctx context.Context, a PoolConfirmArgs) (PoolCreateResult, *Error) {
	var none PoolCreateResult
	if !storage.ValidPoolName(a.Name) {
		return none, errorf(CodePoolName, "invalid pool name")
	}
	if a.Confirm != a.Name {
		return none, errorf(CodeConfirm, "confirmation does not match the pool name")
	}
	if o.Files.Exists(storage.MountPoint(a.Name)) && o.mountInfo(ctx, storage.MountPoint(a.Name)).Mounted {
		return none, errorf(CodePoolExists, "something is already mounted on %s", storage.MountPoint(a.Name))
	}
	scan, err := o.poolsScan(ctx)
	if err != nil {
		return none, err
	}
	var cand *ImportCandidate
	for i := range scan.Pools {
		if scan.Pools[i].Name == a.Name {
			cand = &scan.Pools[i]
		}
	}
	if cand == nil {
		return none, errorf(CodePoolNotFound, "no importable pool named %s", a.Name)
	}
	if !cand.Complete {
		return none, errorf(CodePoolIncomplete, "pool %s has %d of its %d disks", a.Name, cand.Present, cand.Expected).
			with(map[string]any{"present": cand.Present, "expected": cand.Expected})
	}
	groups, err := o.importGroups(ctx)
	if err != nil {
		return none, err
	}
	g := groups[a.Name]
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	x := &poolBuild{o: o, ctx: ctx, name: a.Name}

	fsDevice, md := "/dev/"+g.parts[0], ""
	if g.kind == "md" {
		md = storage.MDDevice(a.Name)
		if err := o.assembleForImport(ctx, a.Name, g); err != nil {
			return none, err
		}
		x.mdCreated = true // stop it again on rollback
		res, rerr := o.Run.Run(ctx, "mdadm", "--detail", "--brief", md)
		line := strings.TrimSpace(string(res.Stdout))
		if rerr != nil || !strings.HasPrefix(line, "ARRAY ") {
			x.rollback()
			return none, errorf(CodeFailed, "mdadm --detail --brief: %v", rerr)
		}
		x.removeMDConf() // a stale block from a previous life
		if err := x.addMDConf(line); err != nil {
			x.rollback()
			return none, err
		}
		fsDevice = md
	}
	res, rerr := o.Run.Run(ctx, "blkid", "-p", "-o", "export", fsDevice)
	props := storage.ParseMDExport(string(res.Stdout))
	if rerr != nil || props["TYPE"] != "btrfs" || props["LABEL"] != storage.FSLabel(a.Name) || !fsUUIDRe.MatchString(props["UUID"]) {
		x.rollback()
		return none, errorf(CodeFailed, "%s does not hold the pool's Btrfs filesystem", fsDevice)
	}
	uuid := props["UUID"]

	mnt := storage.MountPoint(a.Name)
	if err := o.Files.MkdirAll(mnt, 0o755); err != nil {
		x.rollback()
		return none, errorf(CodeFailed, "create %s: %v", mnt, err)
	}
	x.mountDir = true
	unitName := storage.MountUnitName(a.Name)
	if err := o.Files.WriteFile(path.Join(o.UnitDir, unitName), []byte(storage.MountUnit(a.Name, uuid)), 0o644); err != nil {
		x.rollback()
		return none, errorf(CodeFailed, "write unit: %v", err)
	}
	x.unitWritten = true
	_ = o.check(ctx, "systemctl", "daemon-reload")
	if err := o.check(ctx, "systemctl", "enable", "--now", unitName); err != nil {
		x.rollback()
		return none, err
	}
	x.unitEnabled = true
	if m := o.mountInfo(ctx, mnt); !m.Mounted || m.UUID != uuid {
		x.rollback()
		return none, errorf(CodeFailed, "pool not mounted as expected")
	}

	layout := cand.Layout
	if g.kind == "btrfs" {
		layout = storage.LayoutSingle
		if res, err := o.Run.Run(ctx, "btrfs", "--format", "json", "filesystem", "df", mnt); err == nil {
			if df, err := storage.ParseFilesystemDF(res.Stdout); err == nil {
				switch data, _ := storage.TargetProfiles(df); data {
				case "raid1":
					layout = storage.LayoutMirror
				case "raid10":
					layout = storage.LayoutRAID10
				}
			}
		}
	}
	members := make([]string, 0, len(g.disks))
	for _, d := range g.disks {
		members = append(members, d.ID)
	}
	return PoolCreateResult{Name: a.Name, Layout: layout, UUID: uuid, MDDevice: md, Members: members, MountPoint: mnt}, nil
}

// assembleForImport brings up the pool's md array under /dev/md/cwnsb-<name>.
// udev may already have assembled it, under that name or, coming from another
// host, as /dev/md/<oldhost>:cwnsb-<name>; that one is stopped first.
func (o *Ops) assembleForImport(ctx context.Context, name string, g *importGroup) *Error {
	md := storage.MDDevice(name)
	want := hexOnly(g.uuid)
	if o.Files.Exists(md) {
		return nil
	}
	if b, err := fs.ReadFile(o.FS, "proc/mdstat"); err == nil {
		for kernel := range storage.ParseMDStat(string(b)) {
			res, err := o.Run.Run(ctx, "mdadm", "--detail", "--export", "/dev/"+kernel)
			if err != nil || hexOnly(storage.ParseMDExport(string(res.Stdout))["MD_UUID"]) != want {
				continue
			}
			if o.mountInfoBySource(ctx, "/dev/"+kernel) {
				return errorf(CodePoolInUse, "the array of pool %s is already mounted elsewhere", name)
			}
			if err := o.check(ctx, "mdadm", "--stop", "/dev/"+kernel); err != nil {
				return err
			}
		}
	}
	arrayUUID := strings.ReplaceAll(g.uuid, "-", "")
	if len(arrayUUID) == 32 { // mdadm writes 8:8:8:8
		arrayUUID = arrayUUID[0:8] + ":" + arrayUUID[8:16] + ":" + arrayUUID[16:24] + ":" + arrayUUID[24:32]
	}
	if err := o.checkTimeout(ctx, 2*time.Minute, "mdadm", "--assemble", "--run", md, "--uuid="+arrayUUID, "--config=/dev/null"); err != nil {
		return err
	}
	x := &poolBuild{o: o, ctx: ctx, name: name}
	if !x.waitFor(md) {
		return errorf(CodeFailed, "array %s did not appear", md)
	}
	_ = o.check(ctx, "udevadm", "settle")
	return nil
}

// mountInfoBySource reports whether a block device is mounted anywhere.
func (o *Ops) mountInfoBySource(ctx context.Context, dev string) bool {
	res, err := o.Run.Run(ctx, "findmnt", "-n", "--source", dev)
	return err == nil && res.ExitCode == 0 && strings.TrimSpace(string(res.Stdout)) != ""
}
