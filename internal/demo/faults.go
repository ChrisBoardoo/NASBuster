// SPDX-License-Identifier: AGPL-3.0-or-later

package demo

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/crosswax/nasbuster/internal/storage"
	"github.com/crosswax/nasbuster/internal/sysexec"
)

// Disk failures for the demo and the tests (ADR-0012). The behaviours mirror
// what the lab showed on a real kernel (tools/lab/probe-degraded.sh): a Btrfs
// mirror missing a disk does not mount without -o degraded, writes made while
// degraded leave "single" chunks, a replace then a convert balance restore
// the redundancy; an md array keeps running degraded and rebuilds after --add.

// Demo durations of the background jobs.
const (
	simReplaceTime  = 90 * time.Second
	simBalanceTime  = 30 * time.Second
	simRecoveryTime = 60 * time.Second
	simRepairTime   = 45 * time.Second
)

type simReplace struct {
	devid    int
	disk     string // kernel name of the new disk
	started  time.Time
	finished time.Time
}

type simRecovery struct {
	disk    string
	started time.Time
}

// Fault simulates a hardware event on a disk, named by its /dev/disk/by-id
// basename: "unplug", "replug", "errors" (I/O errors on a pool member),
// "foreign", "usbkey", or "residual" (an old file system behind a removed table).
func (s *System) Fault(kind, id string) error {
	link := "/dev/disk/by-id/" + id
	s.mu.Lock()
	k, ok := s.byID[link]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("demo: no disk %s", id)
	}
	p := s.sim
	p.mu.Lock()
	defer p.mu.Unlock()
	switch kind {
	case "unplug":
		p.unplugged[k] = true
		s.setDevice(link, false)
		for _, a := range p.arrays {
			if contains(a.members, k) && !a.repair.IsZero() {
				// md stops a repair when a member drops, and keeps where in
				// md/sync_min (a third of the way here).
				a.repair, a.syncMin = time.Time{}, "2604590421"
			}
		}
		for _, pool := range p.pools {
			if pool.md == "" && contains(pool.members, k) {
				// What the user meets after the reboot that follows: the
				// mount unit fails, the pool waits for a decision.
				pool.mounted, pool.degraded = false, false
			}
		}
	case "replug":
		delete(p.unplugged, k)
		s.setDevice(link, true)
		for _, pool := range p.pools {
			if a := p.arrays[pool.md]; a != nil && contains(a.members, k) {
				// udev's mdadm --incremental does not put it back into the
				// running array: the page offers "Take the disk back" (ADR-0030).
				if a.out == nil {
					a.out = map[string]bool{}
				}
				a.out[k] = true
				continue
			}
			if pool.md != "" || !contains(pool.members, k) {
				continue
			}
			switch {
			case !pool.mounted && p.complete(pool):
				pool.mounted = true
			case pool.mounted && pool.degraded:
				// Mounted without it: Btrfs does not reopen it (ADR-0027),
				// the page offers "Take the disk back".
				if pool.unused == nil {
					pool.unused = map[string]bool{}
				}
				pool.unused[k] = true
			}
		}
	case "usbkey":
		p.usbKey(k)
	case "foreign":
		// The disk joins an old mirror "oldnas" from another CWNSB NAS (or
		// from before a reinstall): partitioned and formatted, unknown here.
		pool := p.pools["oldnas"]
		if pool == nil {
			pool = &simPool{name: "oldnas", layout: storage.LayoutMirror, uuid: uuidFor("oldnas"), errs: map[string]int64{}}
			p.pools["oldnas"] = pool
		}
		if !contains(pool.members, k) {
			pool.members = append(pool.members, k)
		}
		p.partitioned[k] = "cwnsb-oldnas"
	case "residual":
		// Another tool removed only the partition table (OMV's quick wipe,
		// sgdisk --zap-all): blank for every probe of the disk, but the old
		// NTFS reappears inside a new partition that starts at the same place.
		if node := s.baseNode(k); node != nil {
			delete(node, "children")
			node["pttype"] = nil
		}
		p.residual[k] = "ntfs"
	case "errors":
		for _, pool := range p.pools {
			if contains(pool.members, k) {
				pool.errs[k] += 4096
			}
		}
	default:
		return fmt.Errorf("demo: unknown fault %q", kind)
	}
	return nil
}

func (s *System) setDevice(link string, present bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.devices[link] = present
}

// complete reports whether every member of a pool is plugged in.
func (p *poolSim) complete(pool *simPool) bool {
	for _, k := range pool.members {
		if p.unplugged[k] {
			return false
		}
	}
	return true
}

func (p *poolSim) poolByUUID(uuid string) *simPool {
	for _, pool := range p.pools {
		if pool.uuid == uuid {
			return pool
		}
	}
	return nil
}

// progress ends the background jobs whose time has come.
func (p *poolSim) progress(pool *simPool) {
	now := p.sys.Clock()
	if r := pool.replace; r != nil && r.finished.IsZero() && now.Sub(r.started) >= simReplaceTime {
		pool.members[r.devid-1] = r.disk
		r.finished = now
	}
	if !pool.balance.IsZero() && now.Sub(pool.balance) >= simBalanceTime {
		pool.balance, pool.mixed = time.Time{}, false
	}
	if pool.md != "" {
		a := p.arrays[pool.md]
		if r := a.recovery; r != nil && now.Sub(r.started) >= simRecoveryTime {
			a.members = append(a.members, r.disk)
			a.recovery = nil
			pool.members = append([]string(nil), a.members...)
		}
		if !a.repair.IsZero() && now.Sub(a.repair) >= simRepairTime {
			a.repair, a.syncMin = time.Time{}, "0"
		}
	}
}

// show renders `btrfs filesystem show --raw` (formats from the lab).
func (p *poolSim) show(pool *simPool) sysexec.Result {
	var b strings.Builder
	fmt.Fprintf(&b, "Label: 'cwnsb-%s'  uuid: %s\n", pool.name, pool.uuid)
	devices := pool.members
	if pool.md != "" {
		devices = []string{p.arrays[pool.md].kernel}
	}
	fmt.Fprintf(&b, "\tTotal devices %d FS bytes used %d\n", len(devices), p.used(pool))
	present, missing := 0, false
	for i, k := range devices {
		if p.unplugged[k] {
			missing = true
			if pool.mounted {
				fmt.Fprintf(&b, "\tdevid %4d size 0 used 0 path  MISSING\n", i+1)
			}
			continue
		}
		present++
		path, size := "/dev/"+k+"1", storage.PartitionBytes(p.sys.baseSize(k))
		if pool.md != "" {
			path, size = "/dev/"+k, p.usable(pool)
		}
		fmt.Fprintf(&b, "\tdevid %4d size %d used %d path %s\n", i+1, size, p.used(pool)/uint64(len(devices)), path)
	}
	if missing && !pool.mounted {
		b.WriteString("\t*** Some devices missing\n")
	}
	if present == 0 {
		return sysexec.Result{ExitCode: 1, Stderr: []byte("ERROR: no device found")}
	}
	return sysexec.Result{Stdout: []byte(b.String())}
}

// btrfsRepair handles the replace, balance and resize commands.
func (p *poolSim) btrfsRepair(pool *simPool, args []string) (sysexec.Result, bool) {
	mnt := args[len(args)-1]
	now := p.sys.Clock()
	switch {
	case len(args) >= 5 && args[0] == "replace" && args[1] == "start":
		var devid int
		fmt.Sscanf(args[len(args)-3], "%d", &devid)
		disk := p.partDisk(args[len(args)-2])
		if devid < 1 || devid > len(pool.members) || disk == "" {
			r, _ := fail("bad replace arguments")
			return r, true
		}
		pool.replace = &simReplace{devid: devid, disk: disk, started: now}
		return done, true
	case len(args) == 4 && args[0] == "replace" && args[1] == "status":
		r := pool.replace
		switch {
		case r == nil:
			return sysexec.Result{Stdout: []byte("Never started\n")}, true
		case r.finished.IsZero():
			pct := 100 * now.Sub(r.started).Seconds() / simReplaceTime.Seconds()
			return sysexec.Result{Stdout: []byte(fmt.Sprintf("%.1f%% done, 0 write errs, 0 uncorr. read errs\n", pct))}, true
		default:
			return sysexec.Result{Stdout: []byte(fmt.Sprintf("Started on %s, finished on %s, 0 write errs, 0 uncorr. read errs\n",
				r.started.Local().Format("_2.Jan 15:04:05"), r.finished.Local().Format("_2.Jan 15:04:05")))}, true
		}
	case len(args) >= 3 && args[0] == "balance" && args[1] == "start":
		if pool.mixed {
			pool.balance = now
		}
		return done, true
	case len(args) == 3 && args[0] == "balance" && args[1] == "status":
		if pool.balance.IsZero() {
			return sysexec.Result{Stdout: []byte("No balance found on '" + mnt + "'\n")}, true
		}
		left := 100 - int(100*now.Sub(pool.balance).Seconds()/simBalanceTime.Seconds())
		return sysexec.Result{ExitCode: 1, Stdout: []byte(fmt.Sprintf("Balance on '%s' is running\n%d out of about 12 chunks balanced (%d considered), %3d%% left\n",
			mnt, 12-12*left/100, 12-12*left/100+1, left))}, true
	case len(args) == 4 && args[0] == "filesystem" && args[1] == "resize":
		return done, true
	case len(args) == 4 && args[0] == "device" && args[1] == "stats" && args[2] == "-z":
		pool.errs = map[string]int64{}
		return done, true
	}
	return sysexec.Result{}, false
}

// deviceStats renders `btrfs --format json device stats`, missing and failing devices included.
func (p *poolSim) deviceStats(pool *simPool) sysexec.Result {
	var devs []map[string]any
	if pool.md != "" {
		devs = append(devs, statsEntry("/dev/"+p.arrays[pool.md].kernel, 1, 0))
	} else {
		for i, k := range pool.members {
			name := "/dev/" + k + "1"
			if p.unplugged[k] {
				name = fmt.Sprintf("devid:%d", i+1)
			}
			devs = append(devs, statsEntry(name, i+1, pool.errs[k]))
		}
	}
	b, _ := json.Marshal(map[string]any{"__header": map[string]string{"version": "1"}, "device-stats": devs})
	return sysexec.Result{Stdout: b}
}

func statsEntry(device string, devid int, writeErrs int64) map[string]any {
	return map[string]any{"device": device, "devid": devid, "write_io_errs": writeErrs, "read_io_errs": 0,
		"flush_io_errs": 0, "corruption_errs": 0, "generation_errs": 0}
}

// mount handles `mount -t btrfs -o <opts> /dev/disk/by-uuid/<uuid> <mnt>`.
func (p *poolSim) mount(args []string) (sysexec.Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if res, handled := p.mountImport(args); handled {
		return res, nil
	}
	if len(args) != 6 || args[0] != "-t" || args[2] != "-o" {
		return fail("unsupported mount call")
	}
	pool := p.poolByUUID(strings.TrimPrefix(args[4], "/dev/disk/by-uuid/"))
	if pool == nil || storage.MountPoint(pool.name) != args[5] {
		return fail("no such filesystem")
	}
	degraded := strings.Contains(","+args[3]+",", ",degraded,")
	if !p.complete(pool) && !degraded {
		return sysexec.Result{ExitCode: 32, Stderr: []byte("mount: wrong fs type, bad option, bad superblock")}, nil
	}
	pool.mounted, pool.degraded = true, degraded
	if !p.complete(pool) {
		pool.mixed = true // writes while degraded land in "single" chunks
	}
	return done, nil
}
