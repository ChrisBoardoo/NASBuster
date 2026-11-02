// SPDX-License-Identifier: AGPL-3.0-or-later

package demo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io/fs"
	"path"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/crosswax/nasbuster/internal/storage"
	"github.com/crosswax/nasbuster/internal/sysexec"
)

// poolSim emulates the pool tools so the real helper code (sgdisk, mdadm,
// mkfs.btrfs, systemctl, findmnt, btrfs, blkid…) runs unchanged in demo mode.
type poolSim struct {
	sys *System

	mu          sync.Mutex
	partitioned map[string]string    // disk kernel name -> partition label
	arrays      map[string]*simArray // md path (/dev/md/cwnsb-x) -> array
	pools       map[string]*simPool  // pool name -> pool
	nextMD      int
	unplugged   map[string]bool   // disk kernel names pulled out (Fault)
	residual    map[string]string // disk kernel name -> file system left where a partition starts (Fault)
}

type simArray struct {
	kernel   string // md127
	level    int
	members  []string // disk kernel names
	expected int
	recovery *simRecovery
	out      map[string]bool // came back after udev removed it: outside until --re-add (ADR-0030)
	stopped  bool            // mdadm --stop by kernel name, until --assemble (ADR-0032)
	repair   time.Time       // parity repair start (zero: none)
	// syncMin is md/sync_min: where a repair stopped halfway (a member that
	// dropped), "0" once one completes. A new array (assembled) starts at "0".
	syncMin string
}

func (a *simArray) levelName() string { return fmt.Sprintf("raid%d", a.level) }

// arrayUUID is the demo's UUID for an array (mdadm --detail, --examine).
func arrayUUID(md string) string {
	return fmt.Sprintf("%08x:%08x:%08x:%08x", hash32(md), hash32(md+"1"), hash32(md+"2"), hash32(md+"3"))
}

// writeSysfs takes the md attributes the helper writes before a repair.
func (p *poolSim) writeSysfs(path string, value []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	rest, ok := strings.CutPrefix(path, "/sys/block/")
	kernel, attr, _ := strings.Cut(rest, "/md/")
	if !ok || (attr != "sync_min" && attr != "sync_max") {
		return fmt.Errorf("demo: cannot write %s", path)
	}
	for _, a := range p.arrays {
		if a.kernel == kernel && !a.stopped {
			if attr == "sync_min" {
				a.syncMin = string(value)
			}
			return nil
		}
	}
	return fmt.Errorf("demo: no array %s", kernel)
}

// active counts the members running in the array.
func (p *poolSim) active(a *simArray) int {
	n := 0
	for _, k := range a.members {
		if !p.unplugged[k] && !a.out[k] {
			n++
		}
	}
	return n
}

type simPool struct {
	name, layout, uuid string
	md                 string   // md path for parity layouts
	members            []string // disk kernel names
	mounted            bool
	scrubbed           time.Time
	scrubAborted       bool // stopped by "scrub cancel": resumable

	degraded  bool             // mounted with -o degraded
	unused    map[string]bool  // plugged back while mounted degraded: unused until remounted (ADR-0027)
	mixed     bool             // single chunks written while degraded
	replace   *simReplace      // btrfs replace
	balance   time.Time        // convert balance start (zero: none)
	errs      map[string]int64 // write errors per member
	corrected int64            // errors the last scrub repaired
}

func newPoolSim(s *System) *poolSim {
	return &poolSim{sys: s, partitioned: map[string]string{}, arrays: map[string]*simArray{}, pools: map[string]*simPool{},
		unplugged: map[string]bool{}, residual: map[string]string{}}
}

var done = sysexec.Result{} // success, no output

func fail(msg string) (sysexec.Result, error) {
	return sysexec.Result{ExitCode: 1, Stderr: []byte("demo: " + msg)}, nil
}

func (p *poolSim) register(f *sysexec.Fake) {
	f.Handle("lsblk", p.lsblk)
	f.Handle("udevadm", func([]string) (sysexec.Result, error) { return done, nil }) // settle
	f.Handle("wipefs", func([]string) (sysexec.Result, error) { return done, nil })
	f.Handle("dd", p.dd)
	f.Handle("sgdisk", p.sgdisk)
	f.Handle("blkid", p.blkid)
	f.Handle("mdadm", p.mdadm)
	f.Handle("mkfs.btrfs", p.mkfs)
	f.Handle("systemctl", p.systemctl)
	f.Handle("findmnt", p.findmnt)
	f.Handle("btrfs", p.btrfs)
	f.Handle("mount", p.mount)
	f.Handle("umount", p.umount)
	f.Handle("rsync", p.rsync)
}

// kname resolves a by-id path or /dev/<name> to a disk kernel name.
func (p *poolSim) kname(dev string) string {
	if k, ok := p.sys.byID[dev]; ok {
		return k
	}
	return ""
}

// partDisk maps a partition path (by-id …-part1) to its disk kernel name.
func (p *poolSim) partDisk(part string) string {
	return p.kname(strings.TrimSuffix(part, "-part1"))
}

func (p *poolSim) sgdisk(args []string) (sysexec.Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	dev := args[len(args)-1]
	k := p.kname(dev)
	if k == "" {
		return fail("no such device " + dev)
	}
	switch args[0] {
	case "-n":
		label := ""
		for i, a := range args {
			if a == "-c" && i+1 < len(args) {
				label = strings.TrimPrefix(args[i+1], "1:")
			}
		}
		p.partitioned[k] = label
		p.sys.Files.WriteFile(dev+"-part1", nil, 0)
	case "--zap-all":
		delete(p.partitioned, k)
		p.sys.Files.Remove(dev + "-part1")
		if node := p.sys.baseNode(k); node != nil { // pre-formatted disk wiped
			delete(node, "children")
			node["pttype"] = nil
		}
		for name, pool := range p.pools { // the last member erased: the filesystem is gone
			gone := true
			for _, m := range pool.members {
				if _, part := p.partitioned[m]; part {
					gone = false
				}
			}
			if gone && !pool.mounted {
				delete(p.pools, name)
			}
		}
	}
	return done, nil
}

// dd zeroes the start of a disk (erase): an old file system left there goes.
func (p *poolSim) dd(args []string) (sysexec.Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, a := range args {
		if dev, ok := strings.CutPrefix(a, "of="); ok {
			delete(p.residual, p.kname(dev))
		}
	}
	return done, nil
}

func (p *poolSim) blkid(args []string) (sysexec.Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	target := args[len(args)-1]
	if part := p.basePartition(strings.TrimPrefix(target, "/dev/")); part != nil && len(args) >= 4 && args[2] == "export" {
		fstype, _ := part["fstype"].(string)
		label, _ := part["label"].(string)
		uuid, _ := part["uuid"].(string)
		if fstype == "" {
			return sysexec.Result{ExitCode: 2}, nil
		}
		return sysexec.Result{Stdout: []byte("LABEL=" + label + "\nUUID=" + uuid + "\nTYPE=" + fstype + "\n")}, nil
	}
	if len(args) >= 4 && args[1] == "-o" && args[2] == "export" && strings.HasPrefix(target, "/dev/sd") {
		k := strings.TrimSuffix(strings.TrimPrefix(target, "/dev/"), "1")
		label, part := p.partitioned[k]
		if !part || target != "/dev/"+k+"1" {
			return sysexec.Result{ExitCode: 2}, nil
		}
		pool := p.pools[strings.TrimPrefix(label, "cwnsb-")]
		switch {
		case pool == nil && p.arrayOf(k) == nil:
			return sysexec.Result{ExitCode: 2}, nil
		case pool == nil || pool.md != "":
			md := pool.md
			if pool == nil {
				for path, a := range p.arrays {
					if contains(a.members, k) {
						md = path
					}
				}
			}
			return sysexec.Result{Stdout: []byte("UUID=" + memberUUID(md) + "\nTYPE=linux_raid_member\n")}, nil
		default:
			return sysexec.Result{Stdout: []byte("LABEL=" + storage.FSLabel(pool.name) + "\nUUID=" + pool.uuid + "\nTYPE=btrfs\n")}, nil
		}
	}
	if len(args) >= 4 && args[1] == "-o" && args[2] == "export" && strings.HasSuffix(target, "-part1") { // new partition
		k := p.partDisk(target)
		label, part := p.partitioned[k]
		if !part {
			return sysexec.Result{ExitCode: 2}, nil
		}
		// As the real blkid: a partition always reports its table entry.
		entry := "PART_ENTRY_SCHEME=gpt\nPART_ENTRY_NAME=" + label + "\nPART_ENTRY_NUMBER=1\nPART_ENTRY_OFFSET=2048\n"
		if fs := p.residual[k]; fs != "" {
			return sysexec.Result{Stdout: []byte("DEVNAME=" + target + "\nUUID=5C2E8B1F2E8AF6D0\nTYPE=" + fs + "\n" + entry)}, nil
		}
		return sysexec.Result{Stdout: []byte("DEVNAME=" + target + "\n" + entry)}, nil
	}
	if len(args) >= 4 && args[1] == "-o" && args[2] == "export" { // blkid -p -o export <disk>
		k := p.kname(target)
		if _, part := p.partitioned[k]; part {
			return sysexec.Result{Stdout: []byte("PTTYPE=gpt\n")}, nil
		}
		if pt := p.sys.basePTType(k); pt != "" {
			return sysexec.Result{Stdout: []byte("PTTYPE=" + pt + "\n")}, nil
		}
		return sysexec.Result{ExitCode: 2}, nil
	}
	for _, pool := range p.pools { // blkid -p -s UUID -o value <fs device>
		if pool.md == target || (pool.md == "" && contains(pool.members, p.partDisk(target))) {
			return sysexec.Result{Stdout: []byte(pool.uuid + "\n")}, nil
		}
	}
	return sysexec.Result{ExitCode: 2}, nil
}

func (p *poolSim) mdadm(args []string) (sysexec.Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch args[0] {
	case "--create":
		md := args[1]
		a := &simArray{kernel: fmt.Sprintf("md%d", 127-p.nextMD)}
		p.nextMD++
		for _, arg := range args[2:] {
			if v, found := strings.CutPrefix(arg, "--level="); found {
				fmt.Sscanf(v, "%d", &a.level)
			} else if strings.HasPrefix(arg, "/dev/") {
				a.members = append(a.members, p.partDisk(arg))
			}
		}
		a.expected = len(a.members)
		p.arrays[md] = a
		p.sys.Files.WriteFile(md, nil, 0)
	case "--detail":
		md := args[len(args)-1]
		if _, found := p.arrays[md]; !found {
			return fail("no such array")
		}
		return sysexec.Result{Stdout: []byte(fmt.Sprintf("ARRAY %s metadata=1.2 UUID=%s\n", md, arrayUUID(md)))}, nil
	case "--stop":
		if _, found := p.arrays[args[1]]; found { // destroy: by its name
			delete(p.arrays, args[1])
			p.sys.Files.Remove(args[1])
			break
		}
		for md, a := range p.arrays { // reassemble: the broken array, by kernel name
			if "/dev/"+a.kernel == args[1] {
				a.stopped, a.repair, a.syncMin = true, time.Time{}, ""
				p.sys.Files.Remove(md)
			}
		}
	case "--examine":
		return p.examine(args[1])
	case "--action=repair":
		a, found := p.arrays[args[1]]
		if !found || a.stopped {
			return fail("no such array")
		}
		a.repair = p.sys.Clock()
	case "--manage":
		a, found := p.arrays[args[1]]
		if !found || len(args) != 4 {
			return fail("no such array")
		}
		switch args[2] {
		case "--re-add": // the bitmap catches up at once
			k := strings.TrimSuffix(strings.TrimPrefix(args[3], "/dev/"), "1")
			if !a.out[k] {
				return fail("mdadm: --re-add for " + args[3] + " to " + args[1] + " is not possible")
			}
			delete(a.out, k)
		case "--remove": // failed, detached, or a partition
			var keep []string
			for _, k := range a.members {
				gone := p.unplugged[k] && (args[3] == "failed" || args[3] == "detached")
				if !gone && "/dev/"+k+"1" != args[3] {
					keep = append(keep, k)
				}
			}
			a.members = keep
		case "--add":
			k := p.partDisk(args[3])
			if k == "" || a.recovery != nil {
				return fail("cannot add " + args[3])
			}
			a.recovery = &simRecovery{disk: k, started: p.sys.Clock()}
		}
	case "--assemble":
		if len(args) < 4 || args[1] != "--force" {
			return done, nil // demo arrays never stop on their own
		}
		md, uuid := "", ""
		for _, arg := range args[2:] {
			if v, ok := strings.CutPrefix(arg, "--uuid="); ok {
				uuid = v
			} else if strings.HasPrefix(arg, "/dev/md/") && md == "" {
				md = arg
			}
		}
		a, found := p.arrays[md]
		if !found {
			return fail("no such array")
		}
		if uuid != "" && uuid != arrayUUID(md) {
			return fail("mdadm: no devices found for " + md)
		}
		// mdadm forces only what the array needs to start (seen in the lab):
		// the other members stay out until --re-add.
		need := a.expected - storage.MDTolerance(a.levelName(), a.expected)
		for _, k := range a.members {
			if p.active(a) >= need {
				break
			}
			if a.out[k] && !p.unplugged[k] {
				delete(a.out, k)
			}
		}
		a.stopped, a.syncMin = false, ""
		p.sys.Files.WriteFile(md, nil, 0)
	}
	return done, nil
}

// examine renders `mdadm --examine <partition>` (fields from the Pi 5): the
// member still running in a broken array noted the others' departure, so its
// event count is ahead.
func (p *poolSim) examine(part string) (sysexec.Result, error) {
	k := strings.TrimSuffix(strings.TrimPrefix(part, "/dev/"), "1")
	for md, a := range p.arrays {
		role := slices.Index(a.members, k)
		if role < 0 {
			continue
		}
		events, state := 769, strings.Repeat("A", a.expected)
		if a.out[k] {
			events = 763
		} else if p.active(a) < a.expected {
			state = ""
			for _, m := range a.members {
				if p.unplugged[m] || a.out[m] {
					state += "."
				} else {
					state += "A"
				}
			}
		}
		pool := strings.TrimPrefix(md, "/dev/md/")
		return sysexec.Result{Stdout: []byte(fmt.Sprintf(`%s:
          Magic : a92b4efc
        Version : 1.2
    Feature Map : 0x1
     Array UUID : %s
           Name : raspberrypi:%s
     Raid Level : %s
   Raid Devices : %d
         Events : %d
   Device Role : Active device %d
   Array State : %s ('A' == active, '.' == missing, 'R' == replacing)
`, part, arrayUUID(md), pool, a.levelName(), a.expected, events, role, state))}, nil
	}
	return sysexec.Result{ExitCode: 1, Stderr: []byte("mdadm: No md superblock detected on " + part + ".\n")}, nil
}

func (p *poolSim) mkfs(args []string) (sysexec.Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var label, data string
	var targets []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-L":
			i++
			label = args[i]
		case "-d":
			i++
			data = args[i]
		case "-m":
			i++
		default:
			targets = append(targets, args[i])
		}
	}
	name := strings.TrimPrefix(label, "cwnsb-")
	if _, exists := p.pools[name]; exists {
		return fail("filesystem exists")
	}
	pool := &simPool{name: name, uuid: uuidFor(name), errs: map[string]int64{}}
	if a, isMD := p.arrays[targets[0]]; isMD {
		pool.md = targets[0]
		pool.members = a.members
		pool.layout = map[int]string{5: storage.LayoutParity1, 6: storage.LayoutParity2}[a.level]
	} else {
		for _, t := range targets {
			pool.members = append(pool.members, p.partDisk(t))
		}
		pool.layout = map[string]string{"single": storage.LayoutSingle, "raid1": storage.LayoutMirror, "raid10": storage.LayoutRAID10}[data]
	}
	p.pools[name] = pool
	return done, nil
}

func poolFromUnit(unit string) string {
	return strings.TrimSuffix(strings.TrimPrefix(unit, "srv-cwnsb-"), ".mount")
}

func (p *poolSim) systemctl(args []string) (sysexec.Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(args) == 3 && (args[0] == "enable" || args[0] == "disable") {
		if pool, found := p.pools[poolFromUnit(args[2])]; found {
			pool.mounted = args[0] == "enable"
		}
	}
	if len(args) == 2 && (args[0] == "start" || args[0] == "stop") {
		if pool, found := p.pools[poolFromUnit(args[1])]; found {
			if args[0] == "start" && pool.md == "" && !p.complete(pool) {
				return sysexec.Result{ExitCode: 1, Stderr: []byte("Job for " + args[1] + " failed")}, nil
			}
			pool.mounted, pool.degraded = args[0] == "start", false
			pool.unused = nil // a fresh mount opens every disk present
		}
	}
	return done, nil
}

func (p *poolSim) mountedPool(mnt string) *simPool {
	for _, pool := range p.pools {
		if pool.mounted && storage.MountPoint(pool.name) == mnt {
			return pool
		}
	}
	return nil
}

func (p *poolSim) findmnt(args []string) (sysexec.Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	mnt := args[len(args)-1]
	pool := p.mountedPool(mnt)
	if pool == nil {
		if part := p.importMount(mnt); part != nil { // a foreign disk, read-only (ADR-0015)
			b, _ := json.Marshal(map[string]any{"filesystems": []map[string]any{{"target": mnt, "source": "/dev/" + part["kname"].(string),
				"fstype": part["fstype"], "uuid": part["uuid"], "options": "ro,nosuid,nodev,noexec,relatime"}}})
			return sysexec.Result{Stdout: b}, nil
		}
		return sysexec.Result{ExitCode: 1}, nil
	}
	opts := "rw,noatime,compress=zstd:1,space_cache=v2,subvolid=5,subvol=/"
	if pool.degraded {
		opts = "rw,noatime,compress=zstd:1,degraded,space_cache=v2,subvolid=5,subvol=/"
	}
	b, _ := json.Marshal(map[string]any{"filesystems": []map[string]string{
		{"target": mnt, "source": p.fsDevices(pool)[0], "fstype": "btrfs", "uuid": pool.uuid, "options": opts}}})
	return sysexec.Result{Stdout: b}, nil
}

// fsDevices are the block devices holding the Btrfs filesystem.
func (p *poolSim) fsDevices(pool *simPool) []string {
	if pool.md != "" {
		return []string{"/dev/" + p.arrays[pool.md].kernel}
	}
	out := make([]string, len(pool.members))
	for i, k := range pool.members {
		out[i] = "/dev/" + k + "1"
	}
	return out
}

// usable is the pool's capacity as the planner computes it.
func (p *poolSim) usable(pool *simPool) uint64 {
	sizes := make([]uint64, len(pool.members))
	for i, k := range pool.members {
		sizes[i] = p.sys.baseSize(k)
	}
	plan, err := storage.PlanPool(pool.layout, sizes)
	if err != nil {
		return 0
	}
	return plan.UsableBytes
}

func (p *poolSim) btrfs(args []string) (sysexec.Result, error) {
	if len(args) > 0 && args[0] == "subvolume" {
		return p.sys.shares.subvolume(args)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	mnt := args[len(args)-1]
	pool := p.mountedPool(mnt)
	if pool == nil {
		if len(args) == 4 && args[0] == "filesystem" && args[1] == "show" { // by uuid, unmounted
			if byUUID := p.poolByUUID(mnt); byUUID != nil {
				return p.show(byUUID), nil
			}
			return sysexec.Result{ExitCode: 1}, nil
		}
		return fail("not a btrfs mount: " + mnt)
	}
	p.progress(pool)
	if res, handled := p.btrfsRepair(pool, args); handled {
		return res, nil
	}
	cmd := strings.Join(args[:len(args)-1], " ")
	switch cmd {
	case "filesystem show --raw":
		return p.show(pool), nil
	case "--format json device stats":
		return p.deviceStats(pool), nil
	case "--format json filesystem df":
		data, meta := "single", "DUP"
		switch pool.layout {
		case storage.LayoutMirror:
			data, meta = "RAID1", "RAID1"
			if len(pool.members) >= 3 {
				meta = "RAID1C3"
			}
		case storage.LayoutRAID10:
			data, meta = "RAID10", "RAID1C3"
		}
		used := p.used(pool)
		df := []map[string]any{
			{"bg-type": "Data", "bg-profile": data, "total": used + 1<<30, "used": used},
			{"bg-type": "System", "bg-profile": meta, "total": 8 << 20, "used": 16 << 10},
			{"bg-type": "Metadata", "bg-profile": meta, "total": 1 << 30, "used": used / 200},
			{"bg-type": "GlobalReserve", "bg-profile": "single", "total": 512 << 20, "used": 0},
		}
		if pool.mixed {
			df = append(df[:3], map[string]any{"bg-type": "Data", "bg-profile": "single", "total": 2 << 30, "used": 1 << 30},
				map[string]any{"bg-type": "Metadata", "bg-profile": "single", "total": 256 << 20, "used": 1 << 20}, df[3])
		}
		b, _ := json.Marshal(map[string]any{"__header": map[string]string{"version": "1"}, "filesystem-df": df})
		return sysexec.Result{Stdout: b}, nil
	case "filesystem usage -b":
		var raw, missing uint64
		for _, k := range pool.members {
			raw += storage.PartitionBytes(p.sys.baseSize(k))
			if p.unplugged[k] && pool.md == "" {
				missing += storage.PartitionBytes(p.sys.baseSize(k))
			}
		}
		usable, used := p.usable(pool), p.used(pool)
		return sysexec.Result{Stdout: []byte(fmt.Sprintf("Overall:\n    Device size:\t\t%d\n    Device missing:\t\t%d\n    Used:\t\t\t%d\n    Free (estimated):\t\t%d\t(min: %d)\n",
			raw, missing, used, usable-used, usable-used))}, nil
	case "scrub status -R":
		if pool.scrubbed.IsZero() {
			return sysexec.Result{Stdout: []byte("UUID:             " + pool.uuid + "\n\tno stats available\n")}, nil
		}
		if pool.scrubAborted {
			return sysexec.Result{Stdout: []byte(fmt.Sprintf("UUID:             %s\nScrub started:    %s\nStatus:           aborted\nDuration:         0:00:20\n\tcorrected_errors: 0\n\tuncorrectable_errors: 0\n\tcsum_errors: 0\n\tread_errors: 0\n",
				pool.uuid, pool.scrubbed.Local().Format("Mon Jan  2 15:04:05 2006")))}, nil
		}
		if p.sys.Clock().Sub(pool.scrubbed) < time.Minute {
			return sysexec.Result{Stdout: []byte(fmt.Sprintf("UUID:             %s\nScrub started:    %s\nStatus:           running\nDuration:         0:00:%02d\n\tcorrected_errors: 0\n\tuncorrectable_errors: 0\n\tcsum_errors: 0\n\tread_errors: 0\n",
				pool.uuid, pool.scrubbed.Local().Format("Mon Jan  2 15:04:05 2006"), int(p.sys.Clock().Sub(pool.scrubbed).Seconds())))}, nil
		}
		return sysexec.Result{Stdout: []byte(fmt.Sprintf("UUID:             %s\nScrub started:    %s\nStatus:           finished\nDuration:         0:41:07\n\tcorrected_errors: %d\n\tuncorrectable_errors: 0\n\tcsum_errors: %d\n\tread_errors: 0\n",
			pool.uuid, pool.scrubbed.Local().Format("Mon Jan  2 15:04:05 2006"), pool.corrected, pool.corrected))}, nil
	case "scrub start", "scrub start -B", "scrub resume -B":
		pool.scrubbed, pool.scrubAborted = p.sys.Clock(), false
		pool.corrected = 0
		for _, n := range pool.errs {
			pool.corrected += n // the mirror copy repairs what the failing disk lost
		}
		return done, nil
	case "scrub cancel":
		if !pool.scrubbed.IsZero() && p.sys.Clock().Sub(pool.scrubbed) < time.Minute {
			pool.scrubAborted = true
		}
		return done, nil
	}
	return fail("unsupported btrfs command: " + cmd)
}

// used grows slowly so the dashboard shows a living pool.
func (p *poolSim) used(pool *simPool) uint64 {
	return 412<<30 + uint64(p.sys.Clock().Unix()%600)<<20
}

// addFiles exposes the simulated kernel state (sysfs, /proc/mdstat, /dev/md links).
func (p *poolSim) addFiles(file func(name, content string), link func(name, target string)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, k := range p.sys.baseDisks() {
		file("sys/block/"+k+"/size", "0\n")
		for _, part := range p.sys.basePartitions(k) {
			file("sys/block/"+k+"/"+part+"/size", "0\n")
		}
	}
	for k := range p.partitioned {
		file("sys/block/"+k+"/"+k+"1/size", "0\n")
		link("sys/class/block/"+k+"1", "../../devices/platform/scb/fd500000.pcie/usb2/block/"+k+"/"+k+"1")
	}
	for _, pool := range p.pools {
		p.progress(pool)
		if !pool.mounted || pool.md != "" {
			continue
		}
		for i, k := range pool.members {
			missing := "0\n"
			if p.unplugged[k] {
				missing = "1\n"
			} else if !pool.unused[k] { // a disk back but unused has no entry, as on the Pi
				file("sys/fs/btrfs/"+pool.uuid+"/devices/"+k+"1", "")
			}
			file(fmt.Sprintf("sys/fs/btrfs/%s/devinfo/%d/missing", pool.uuid, i+1), missing)
		}
	}
	var mdstat strings.Builder
	mdstat.WriteString("Personalities : [raid1] [raid6] [raid5] [raid4] \n")
	mds := make([]string, 0, len(p.arrays))
	for md := range p.arrays {
		mds = append(mds, md)
	}
	sort.Strings(mds)
	for _, md := range mds {
		a := p.arrays[md]
		for _, pool := range p.pools {
			if pool.md == md {
				p.progress(pool)
			}
		}
		if a.stopped {
			continue
		}
		link(strings.TrimPrefix(md, "/"), "../"+a.kernel)
		link("dev/disk/by-id/md-uuid-"+arrayUUID(md), "../../"+a.kernel)
		var members, up []string
		active := 0
		for i, k := range a.members {
			if p.unplugged[k] || a.out[k] {
				continue // md drops a member whose device vanished
			}
			members = append(members, fmt.Sprintf("%s1[%d]", k, i))
			active++
		}
		if a.recovery != nil {
			members = append(members, fmt.Sprintf("%s1[%d]", a.recovery.disk, a.expected+1))
		}
		for i := 0; i < a.expected; i++ {
			if i < active {
				up = append(up, "U")
			} else {
				up = append(up, "_")
			}
		}
		// More members gone than the level tolerates: "broken", as on the Pi 5.
		state := "active"
		if a.expected-active > storage.MDTolerance(a.levelName(), a.expected) {
			state = "broken"
		}
		fmt.Fprintf(&mdstat, "%s : %s raid%d %s\n      7813771264 blocks super 1.2 level %d, 512k chunk, algorithm 2 [%d/%d] [%s]\n",
			a.kernel, state, a.level, strings.Join(members, " "), a.level, a.expected, active, strings.Join(up, ""))
		action := "idle\n"
		if r := a.recovery; r != nil {
			pct := 100 * p.sys.Clock().Sub(r.started).Seconds() / simRecoveryTime.Seconds()
			fmt.Fprintf(&mdstat, "      [=>...................]  recovery = %.1f%% (0/3906885632) finish=1.0min speed=150000K/sec\n", pct)
			action = "recover\n"
		} else if !a.repair.IsZero() {
			// The kernel calls a repair "resync" here; only sysfs says "repair".
			pct := 100 * p.sys.Clock().Sub(a.repair).Seconds() / simRepairTime.Seconds()
			fmt.Fprintf(&mdstat, "      [==>..................]  resync = %.1f%% (0/3906885632) finish=0.5min speed=150000K/sec\n", pct)
			action = "repair\n"
		}
		file("sys/block/"+a.kernel+"/md/sync_action", action)
		syncMin := a.syncMin
		if syncMin == "" {
			syncMin = "0"
		}
		file("sys/block/"+a.kernel+"/md/sync_min", syncMin+"\n")
		mdstat.WriteString("      bitmap: 0/30 pages [0KB], 65536KB chunk\n\n")
	}
	mdstat.WriteString("unused devices: <none>\n")
	file("proc/mdstat", mdstat.String())
}

// lsblk renders the base tree plus what the simulation changed.
func (p *poolSim) lsblk([]string) (sysexec.Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var devices []map[string]any
	for _, dev := range cloneTree(p.sys.lsblk) {
		if k, _ := dev["kname"].(string); !p.unplugged[k] {
			devices = append(devices, dev)
		}
	}
	for _, dev := range devices {
		k, _ := dev["kname"].(string)
		label, part := p.partitioned[k]
		if !part {
			continue
		}
		size, _ := dev["size"].(json.Number).Int64()
		child := map[string]any{"name": k + "1", "kname": k + "1", "path": "/dev/" + k + "1", "partlabel": label,
			"size": json.Number(fmt.Sprint(storage.PartitionBytes(uint64(size)))), "type": "part",
			"tran": nil, "rota": dev["rota"], "rm": false, "ro": false, "model": nil, "serial": nil, "wwn": nil, "vendor": nil,
			"fstype": nil, "uuid": nil, "label": nil, "pttype": "gpt", "mountpoints": []any{nil}, "pkname": k, "hotplug": dev["hotplug"]}
		dev["pttype"] = "gpt"
		dev["children"] = []any{child}
		pool := p.pools[strings.TrimPrefix(label, "cwnsb-")]
		if pool == nil {
			if a := p.arrayOf(k); a != nil { // md created, no filesystem yet
				child["fstype"] = "linux_raid_member"
			}
			continue
		}
		if pool.md != "" {
			a := p.arrays[pool.md]
			child["fstype"], child["label"], child["uuid"] = "linux_raid_member", "raspberrypi:"+label, memberUUID(pool.md)
			if a.out[k] {
				continue // outside the running array: no md below it
			}
			md := map[string]any{"name": a.kernel, "kname": a.kernel, "path": "/dev/" + a.kernel,
				"size": json.Number(fmt.Sprint(p.usable(pool))), "type": fmt.Sprintf("raid%d", a.level),
				"fstype": "btrfs", "label": label, "uuid": pool.uuid, "mountpoints": []any{nil}, "pkname": k + "1"}
			if pool.mounted {
				md["mountpoints"] = []any{storage.MountPoint(pool.name)}
			}
			child["children"] = []any{md}
			continue
		}
		child["fstype"], child["label"], child["uuid"] = "btrfs", label, pool.uuid
		if pool.mounted && pool.members[0] == k {
			child["mountpoints"] = []any{storage.MountPoint(pool.name)}
		}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "   ")
	if err := enc.Encode(map[string]any{"blockdevices": devices}); err != nil {
		return fail(err.Error())
	}
	return sysexec.Result{Stdout: buf.Bytes()}, nil
}

// memberUUID is the array UUID that blkid and lsblk show on the members of md,
// the one `mdadm --detail --brief` writes as 8:8:8:8.
func memberUUID(md string) string {
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%04x%08x", hash32(md), hash32(md+"1")>>16, hash32(md+"1")&0xffff,
		hash32(md+"2")>>16, hash32(md+"2")&0xffff, hash32(md+"3"))
}

func (p *poolSim) arrayOf(k string) *simArray {
	for _, a := range p.arrays {
		if contains(a.members, k) {
			return a
		}
	}
	return nil
}

// parseLsblkTree decodes lsblk JSON keeping numbers exact (json.Number).
func parseLsblkTree(b []byte) []map[string]any {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var out struct {
		BlockDevices []map[string]any `json:"blockdevices"`
	}
	if err := dec.Decode(&out); err != nil {
		panic(err) // embedded fixture
	}
	return out.BlockDevices
}

func cloneTree(devs []map[string]any) []map[string]any {
	b, _ := json.Marshal(devs)
	return parseLsblkTree([]byte(`{"blockdevices":` + string(b) + `}`))
}

func (s *System) baseDisks() []string {
	var out []string
	for _, d := range s.lsblk {
		if k, _ := d["kname"].(string); k != "" && d["type"] == "disk" {
			out = append(out, k)
		}
	}
	return out
}

func (s *System) baseNode(k string) map[string]any {
	for _, d := range s.lsblk {
		if d["kname"] == k {
			return d
		}
	}
	return nil
}

func (s *System) basePTType(k string) string {
	if d := s.baseNode(k); d != nil {
		pt, _ := d["pttype"].(string)
		return pt
	}
	return ""
}

func (s *System) basePartitions(k string) []string {
	var out []string
	if d := s.baseNode(k); d != nil {
		children, _ := d["children"].([]any)
		for _, c := range children {
			if m, ok := c.(map[string]any); ok {
				out = append(out, m["kname"].(string))
			}
		}
	}
	return out
}

func (s *System) baseSize(k string) uint64 {
	if d := s.baseNode(k); d != nil {
		if n, ok := d["size"].(json.Number); ok {
			v, _ := n.Int64()
			return uint64(v)
		}
	}
	return 0
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func hash32(s string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(s))
	return h.Sum32()
}

func uuidFor(name string) string {
	h := fnv.New64a()
	h.Write([]byte(name))
	v := h.Sum64()
	return fmt.Sprintf("%08x-%04x-4%03x-8%03x-%012x", hash32(name), v&0xffff, (v>>16)&0xfff, (v>>28)&0xfff, (v>>16)&0xffffffffffff)
}

// Files is an in-memory filesystem for what the helper writes in demo mode
// (same method set as helper.Files).
type Files struct {
	mu    sync.Mutex
	files map[string][]byte
	dirs  map[string]bool
}

// NewFiles returns an empty in-memory filesystem.
func NewFiles() *Files { return &Files{files: map[string][]byte{}, dirs: map[string]bool{"/": true}} }

func (f *Files) Exists(p string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, file := f.files[p]
	return file || f.dirs[p]
}

func (f *Files) ReadFile(p string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, found := f.files[p]
	if !found {
		return nil, fs.ErrNotExist
	}
	return append([]byte(nil), b...), nil
}

func (f *Files) WriteFile(p string, data []byte, _ fs.FileMode) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[p] = append([]byte(nil), data...)
	for d := path.Dir(p); d != "/" && d != "."; d = path.Dir(d) {
		f.dirs[d] = true
	}
	return nil
}

func (f *Files) MkdirAll(p string, _ fs.FileMode) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for d := p; d != "/" && d != "."; d = path.Dir(d) {
		f.dirs[d] = true
	}
	return nil
}

// RemoveAll removes p and everything under it, as deleting a subvolume does.
func (f *Files) RemoveAll(p string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for name := range f.files {
		if name == p || strings.HasPrefix(name, p+"/") {
			delete(f.files, name)
		}
	}
	for d := range f.dirs {
		if d == p || strings.HasPrefix(d, p+"/") {
			delete(f.dirs, d)
		}
	}
}

func (f *Files) MkdirAllIn(root, rel string, perm fs.FileMode) error {
	return f.MkdirAll(path.Join(root, rel), perm)
}

func (f *Files) Remove(p string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, found := f.files[p]; found {
		delete(f.files, p)
		return nil
	}
	if f.dirs[p] {
		delete(f.dirs, p)
		return nil
	}
	return fs.ErrNotExist
}

// ReadDir lists the direct children of p.
func (f *Files) ReadDir(p string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.dirs[p] {
		return nil, fs.ErrNotExist
	}
	set := map[string]bool{}
	for name := range f.files {
		if path.Dir(name) == p {
			set[path.Base(name)] = true
		}
	}
	for d := range f.dirs {
		if d != p && path.Dir(d) == p {
			set[path.Base(d)] = true
		}
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out, nil
}
