// SPDX-License-Identifier: AGPL-3.0-or-later

package helper

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/crosswax/nasbuster/internal/fixture"
	"github.com/crosswax/nasbuster/internal/storage"
	"github.com/crosswax/nasbuster/internal/sysexec"
)

// The kernel's view of a mirror whose disk 2 was pulled out, then came back as
// sdd (measured on the Pi 5, 2026-10-24). Pulled while mounted, its old entry
// (sdc1) stays as a dead link; mounted degraded, it has no entry at all.
const (
	sysDiskBackDeadLink = `-- sys/fs/btrfs/UUID/devices/sdb1 -> ../../../../devices/usb2/sdb/sdb1 --
-- sys/devices/usb2/sdb/sdb1/dev --
8:17
-- sys/fs/btrfs/UUID/devices/sdc1 -> ../../../../devices/usb4/sdc/sdc1 --
`
	sysDiskBackNoEntry = `-- sys/fs/btrfs/UUID/devices/sdb1 -> ../../../../devices/usb2/sdb/sdb1 --
-- sys/devices/usb2/sdb/sdb1/dev --
8:17
`
	sysBothInUse = `-- sys/fs/btrfs/UUID/devices/sdb1 -> ../../../../devices/usb2/sdb/sdb1 --
-- sys/devices/usb2/sdb/sdb1/dev --
8:17
-- sys/fs/btrfs/UUID/devices/sdd1 -> ../../../../devices/usb4/sdd/sdd1 --
-- sys/devices/usb4/sdd/sdd1/dev --
8:49
`
	// smbd still has a client's session in a share of the pool.
	procSambaInShare = `-- proc/4242/cwd -> /srv/cwnsb/tank/photos --
-- proc/4242/cgroup --
0::/system.slice/smbd.service
`
)

type resumeEnv struct {
	run         *sysexec.Fake
	ops         *Ops
	degraded    bool // mounted with -o degraded
	mixed       bool // single chunks written while degraded
	oldName     bool // btrfs shows disk 2 MISSING under its old name (sdc1)
	restartOK   bool // the unmount works (false: target is busy)
	mountFails  bool // the mount with every disk fails
	unmounted   bool
	restarted   bool
	dockerStart bool
}

func newResumeEnv(t *testing.T, sys string) *resumeEnv {
	t.Helper()
	e := &resumeEnv{run: sysexec.NewFake(), restartOK: true}
	files := NewMemFiles()
	files.WriteFile("/etc/systemd/system/srv-cwnsb-tank.mount", []byte(storage.MountUnit("tank", testUUID)), 0o644)
	e.ops = &Ops{Run: e.run, FS: fixture.Parse([]byte(strings.ReplaceAll(sys, "UUID", testUUID))), Logger: quietLogger(),
		Files: files, UnitDir: "/etc/systemd/system", MDConf: "/etc/mdadm/mdadm.conf", Sleep: func(time.Duration) {}}
	mnt := "/srv/cwnsb/tank"
	e.run.Handle("findmnt", func([]string) (sysexec.Result, error) {
		if e.unmounted {
			return sysexec.Result{ExitCode: 1}, nil
		}
		opts := "rw,noatime,compress=zstd:1"
		if e.degraded {
			opts += ",degraded"
		}
		return sysexec.Result{Stdout: []byte(`{"filesystems":[{"target":"` + mnt + `","source":"/dev/sdb1","fstype":"btrfs","uuid":"` + testUUID + `","options":"` + opts + `"}]}`)}, nil
	})
	e.run.Handle("btrfs", func(args []string) (sysexec.Result, error) {
		switch strings.Join(args[:len(args)-1], " ") {
		case "--format json device stats":
			return sysexec.Result{Stdout: []byte(`{"device-stats":[{"device":"/dev/sdb1","devid":1},{"device":"/dev/disk/by-id/usb-Lexar_808-0:0-part1","devid":2,"write_io_errs":2919}]}`)}, nil
		case "--format json filesystem df":
			df := `{"bg-type":"Data","bg-profile":"RAID1","total":3,"used":1},{"bg-type":"Metadata","bg-profile":"RAID1","total":1,"used":1}`
			if e.mixed {
				df += `,{"bg-type":"Data","bg-profile":"single","total":1,"used":1}`
			}
			return sysexec.Result{Stdout: []byte(`{"filesystem-df":[` + df + `]}`)}, nil
		case "filesystem usage -b":
			return sysexec.Result{Stdout: []byte("Overall:\n    Device size:\t\t200\n    Used:\t\t\t20\n    Free (estimated):\t\t80\n")}, nil
		case "scrub status -R":
			return sysexec.Result{Stdout: []byte("Status: finished\n")}, nil
		case "filesystem show --raw":
			disk2 := "\tdevid    2 size 100 used 10 path /dev/sdd1\n"
			if e.oldName && !e.restarted {
				disk2 = "\tdevid    2 size 0 used 0 path /dev/sdc1 MISSING\n"
			}
			return sysexec.Result{Stdout: []byte("Label: 'cwnsb-tank'  uuid: " + testUUID + "\n\tTotal devices 2 FS bytes used 20\n" +
				"\tdevid    1 size 100 used 10 path /dev/sdb1\n" + disk2)}, nil
		case "device":
			return sysexec.Result{}, nil // device scan
		case "replace status -1":
			return sysexec.Result{Stdout: []byte("Never started\n")}, nil
		case "balance status":
			return sysexec.Result{Stdout: []byte("No balance found on '" + mnt + "'\n")}, nil
		}
		return sysexec.Result{ExitCode: 1}, nil
	})
	e.run.Handle("smbcontrol", func([]string) (sysexec.Result, error) { return sysexec.Result{}, nil })
	units := sysexec.NewFake()
	fakeUnits(units, "activating")
	e.run.Handle("systemd-run", func(args []string) (sysexec.Result, error) {
		return units.Run(context.Background(), "systemd-run", args...)
	})
	e.run.Handle("journalctl", func(args []string) (sysexec.Result, error) {
		return units.Run(context.Background(), "journalctl", args...)
	})
	// Both partitions of the pool are plugged in (probed by blkid).
	e.run.Handle("blkid", func([]string) (sysexec.Result, error) {
		return sysexec.Result{Stdout: []byte("/dev/sdd1\n/dev/sdb1\n")}, nil
	})
	const mountUnit = "srv-cwnsb-tank.mount"
	e.run.Handle("systemctl", func(args []string) (sysexec.Result, error) {
		switch {
		case args[0] == "stop" && args[1] == mountUnit:
			if !e.restartOK {
				return sysexec.Result{ExitCode: 1, Stderr: []byte("Job failed. See \"journalctl -xe\" for details.\n")}, nil
			}
			e.unmounted = true
			return sysexec.Result{}, nil
		case args[0] == "start" && args[1] == mountUnit:
			if e.mountFails {
				return sysexec.Result{ExitCode: 1, Stderr: []byte("Job failed.\n")}, nil
			}
			// The mount opens the disk that came back.
			e.unmounted, e.restarted, e.degraded = false, true, false
			e.ops.FS = fixture.Parse([]byte(strings.ReplaceAll(sysBothInUse, "UUID", testUUID)))
			return sysexec.Result{}, nil
		case args[0] == "start" && args[1] == "docker.service":
			e.dockerStart = true
			return sysexec.Result{}, nil
		case args[0] == "show" && args[1] == mountUnit:
			return sysexec.Result{Stdout: []byte("docker.service srv-cwnsb-tank-.docker-overlay2-ab12-merged.mount\n")}, nil
		case args[0] == "is-active" && args[1] == "docker.service":
			return sysexec.Result{Stdout: []byte("active\n")}, nil
		}
		return units.Run(context.Background(), "systemctl", args...)
	})
	return e
}

func TestPoolStatusSeesADiskBackButUnused(t *testing.T) {
	for name, tc := range map[string]struct {
		sys               string
		degraded, oldName bool
	}{
		"pulled while mounted, shown under its new name":  {sysDiskBackDeadLink, false, false},
		"pulled while mounted, MISSING under its old one": {sysDiskBackDeadLink, false, true},
		"mounted degraded": {sysDiskBackNoEntry, true, false},
	} {
		t.Run(name, func(t *testing.T) {
			e := newResumeEnv(t, tc.sys)
			e.degraded, e.oldName = tc.degraded, tc.oldName
			st, err := e.ops.poolStatus(context.Background(), PoolArgs{Name: "tank"})
			if err != nil {
				t.Fatal(err)
			}
			// Not "two disks present": the pool does not use the one that came back.
			if !st.CanResume || st.MissingDevices != 1 || !st.Devices[1].Missing || st.Devices[0].Missing || st.Health != storage.HealthDegraded {
				t.Fatalf("status = %+v", st)
			}
		})
	}

	e := newResumeEnv(t, sysBothInUse)
	st, _ := e.ops.poolStatus(context.Background(), PoolArgs{Name: "tank"})
	if st.CanResume || st.MissingDevices != 0 || st.Devices[1].Missing {
		t.Fatalf("both disks in use = %+v", st)
	}

	// Really gone: nothing to take back, the page offers a replacement.
	e = newResumeEnv(t, sysDiskBackDeadLink)
	e.oldName = true
	e.run.Handle("blkid", func([]string) (sysexec.Result, error) { return sysexec.Result{Stdout: []byte("/dev/sdb1\n")}, nil })
	st, _ = e.ops.poolStatus(context.Background(), PoolArgs{Name: "tank"})
	if st.CanResume || st.MissingDevices != 1 {
		t.Fatalf("disk gone = %+v", st)
	}
}

func TestPoolResume(t *testing.T) {
	e := newResumeEnv(t, sysDiskBackNoEntry)
	e.degraded, e.mixed = true, true

	res, err := e.ops.poolResume(context.Background(), PoolResumeArgs{Name: "tank", Confirm: "tank", Shares: []string{"photos", "docs"}})
	if err != nil || !res.Remounted || !res.ResyncStarted {
		t.Fatalf("resume = %+v %v", res, err)
	}
	calls := e.run.Calls()
	at := func(cmd ...string) int {
		return slices.IndexFunc(calls, func(c []string) bool { return slices.Equal(c, cmd) })
	}
	stop, scan := at("systemctl", "stop", "srv-cwnsb-tank.mount"), at("btrfs", "device", "scan")
	start, docker := at("systemctl", "start", "srv-cwnsb-tank.mount"), at("systemctl", "start", "docker.service")
	closed := 0
	for i, c := range calls {
		if c[0] == "smbcontrol" {
			if i > stop || c[3] != []string{"photos", "docs"}[closed] {
				t.Errorf("shares must be closed before the unmount: %v", calls)
			}
			closed++
		}
	}
	// Unmounted, then the disk is scanned under its new name, then mounted,
	// then Docker (stopped with the mount) runs again.
	if closed != 2 || stop < 0 || !(stop < scan && scan < start && start < docker) {
		t.Fatalf("close-share x%d, stop %d, scan %d, start %d, docker %d: %v", closed, stop, scan, start, docker, calls)
	}
	// Then the single chunks are converted, and a scrub checks both copies.
	var run []string
	for _, c := range calls {
		if c[0] == "systemd-run" {
			run = c[1:]
		}
	}
	if !slices.Contains(run, "--unit=cwnsb-job-resync-tank") ||
		!slices.ContainsFunc(run, func(a string) bool {
			return strings.HasPrefix(a, "ExecStartPre=/usr/bin/btrfs balance start -dconvert=raid1,soft") && strings.HasSuffix(a, " /srv/cwnsb/tank")
		}) ||
		strings.Join(run[len(run)-5:], " ") != "/usr/bin/btrfs scrub start -B /srv/cwnsb/tank" {
		t.Fatalf("resync job = %v", run)
	}
}

func TestPoolResumeRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		sys  string
		args PoolResumeArgs
		code string
	}{
		"confirmation":    {sysDiskBackNoEntry, PoolResumeArgs{Name: "tank", Confirm: "nope"}, CodeConfirm},
		"share name":      {sysDiskBackNoEntry, PoolResumeArgs{Name: "tank", Confirm: "tank", Shares: []string{"../x"}}, CodeInvalidArgs},
		"nothing to take": {sysBothInUse, PoolResumeArgs{Name: "tank", Confirm: "tank"}, CodeNothingToResume},
	} {
		t.Run(name, func(t *testing.T) {
			e := newResumeEnv(t, tc.sys)
			if _, err := e.ops.poolResume(context.Background(), tc.args); err == nil || err.Code != tc.code {
				t.Fatalf("err = %v, want %s", err, tc.code)
			}
			if e.restarted {
				t.Error("the pool was remounted")
			}
		})
	}
}

func TestPoolResumeBusyNamesTheHolder(t *testing.T) {
	e := newResumeEnv(t, sysDiskBackDeadLink+procSambaInShare)
	e.restartOK = false // umount: target is busy, the pool stays mounted

	_, err := e.ops.poolResume(context.Background(), PoolResumeArgs{Name: "tank", Confirm: "tank"})
	if err == nil || err.Code != CodePoolInUse || !strings.Contains(err.Message, "smbd.service") {
		t.Fatalf("busy pool = %v", err)
	}
	// systemd stops the dependents before trying the unmount: they run again.
	if !e.dockerStart || e.restarted {
		t.Fatalf("docker started again: %v, remounted: %v", e.dockerStart, e.restarted)
	}
}

func TestPoolResumeNeverFallsBackToDegraded(t *testing.T) {
	e := newResumeEnv(t, sysDiskBackNoEntry)
	e.degraded, e.mountFails = true, true

	if _, err := e.ops.poolResume(context.Background(), PoolResumeArgs{Name: "tank", Confirm: "tank"}); err == nil {
		t.Fatal("a failed remount must be reported")
	}
	// Left unmounted: a degraded mount now could pick the stale disk that came
	// back (ADR-0012). The page offers the explicit degraded mount instead.
	if !e.unmounted || slices.ContainsFunc(e.run.Calls(), func(c []string) bool { return c[0] == "mount" }) {
		t.Fatalf("unmounted: %v, calls: %v", e.unmounted, e.run.Calls())
	}
}

// A parity pool whose third member came back as sdd1 (ADR-0030). sde1 carries
// an array of the same name from another NAS: it must never be touched.
func newMDResumeEnv(t *testing.T) (*Ops, *sysexec.Fake, *storage.MDStatus) {
	t.Helper()
	run := sysexec.NewFake()
	files := NewMemFiles()
	begin, end := mdConfMarkers("tank")
	files.WriteFile("/etc/mdadm/mdadm.conf", []byte(begin+"\nARRAY /dev/md/cwnsb-tank metadata=1.2 UUID=81c2e2e2:011e9470:36da5ec0:1a272447\n"+end+"\n"), 0o644)
	member := func(name, label, uuid string) storage.Disk {
		return storage.Disk{ID: name, Partitions: []storage.Partition{{Name: name + "1", FSType: "linux_raid_member", Label: label, UUID: uuid}}}
	}
	const ours = "81c2e2e2-011e-9470-36da-5ec01a272447"
	disks := []storage.Disk{
		member("sdb", "Rpi5-NAS:cwnsb-tank", ours),
		member("sdc", "Rpi5-NAS:cwnsb-tank", ours),
		member("sdd", "Rpi5-NAS:cwnsb-tank", ours),
		member("sde", "OtherNAS:cwnsb-tank", "0badc0de-0000-1111-2222-333344445555"),
	}
	run.Handle("blkid", func(args []string) (sysexec.Result, error) {
		return sysexec.Result{Stdout: []byte("DEVNAME=" + args[len(args)-1] + "\nUUID=" + ours + "\nLABEL=Rpi5-NAS:cwnsb-tank\nTYPE=linux_raid_member\n")}, nil
	})
	run.Handle("mdadm", func([]string) (sysexec.Result, error) { return sysexec.Result{}, nil })
	o := &Ops{Run: run, Logger: quietLogger(), Files: files, MDConf: "/etc/mdadm/mdadm.conf",
		Discover: func(context.Context) ([]storage.Disk, error) { return disks, nil }}
	return o, run, &storage.MDStatus{Level: "raid5", Active: 2, Expected: 3, Members: []string{"sdb1", "sdc1"}}
}

func TestMDResume(t *testing.T) {
	for name, tc := range map[string]struct {
		reAddFails bool
		want       string
	}{
		"the bitmap catches up": {false, "--re-add"},
		"full rebuild instead":  {true, "--add"},
	} {
		t.Run(name, func(t *testing.T) {
			o, run, md := newMDResumeEnv(t)
			if got := o.mdReturned(context.Background(), "tank", md); !slices.Equal(got, []string{"sdd1"}) {
				t.Fatalf("returned = %v, want [sdd1]", got)
			}
			run.Handle("mdadm", func(args []string) (sysexec.Result, error) {
				if tc.reAddFails && slices.Contains(args, "--re-add") {
					return sysexec.Result{ExitCode: 1, Stderr: []byte("mdadm: --re-add for /dev/sdd1 to /dev/md/cwnsb-tank is not possible\n")}, nil
				}
				return sysexec.Result{}, nil
			})
			res, err := o.mdResume(context.Background(), "tank", md)
			if err != nil || res.Remounted || !res.ResyncStarted {
				t.Fatalf("resume = %+v %v", res, err)
			}
			var mdadm []string
			for _, c := range run.Calls() {
				switch c[0] {
				case "mdadm":
					mdadm = append(mdadm, strings.Join(c[1:], " "))
				case "systemctl", "umount", "smbcontrol":
					t.Errorf("nothing is stopped or cut: %v", c)
				}
				if slices.Contains(c, "/dev/sde1") {
					t.Errorf("the other NAS's disk was touched: %v", c)
				}
			}
			if mdadm[0] != "--manage /dev/md/cwnsb-tank --remove failed" || mdadm[1] != "--manage /dev/md/cwnsb-tank --remove detached" ||
				mdadm[len(mdadm)-1] != "--manage /dev/md/cwnsb-tank "+tc.want+" /dev/sdd1" {
				t.Fatalf("mdadm = %q", mdadm)
			}
		})
	}
}

func TestMDResumeRefusals(t *testing.T) {
	// udev is late: the partition now holds something else (blkid -p says so).
	o, run, md := newMDResumeEnv(t)
	run.Handle("blkid", func([]string) (sysexec.Result, error) {
		return sysexec.Result{Stdout: []byte("UUID=0badc0de-0000-1111-2222-333344445555\nTYPE=linux_raid_member\n")}, nil
	})
	if _, err := o.mdResume(context.Background(), "tank", md); err == nil || err.Code != CodeNothingToResume {
		t.Fatalf("stale udev: err = %v", err)
	}
	if slices.ContainsFunc(run.Calls(), func(c []string) bool { return c[0] == "mdadm" }) {
		t.Fatalf("mdadm ran: %v", run.Calls())
	}

	// A hub cut took 2 of the 3 members (the Pi 5, 2026-10-26): they come back
	// together, never one by one (an --add would turn them into spares).
	o, run, md = newMDResumeEnv(t)
	md.Active, md.Members = 1, []string{"sdb1"}
	if _, err := o.mdResume(context.Background(), "tank", md); err == nil || err.Code != CodePoolBroken {
		t.Fatalf("broken array: err = %v", err)
	}
	if slices.ContainsFunc(run.Calls(), func(c []string) bool { return c[0] == "mdadm" }) {
		t.Fatalf("mdadm ran on a broken array: %v", run.Calls())
	}

	// A rebuild already runs.
	o, run, md = newMDResumeEnv(t)
	md.Sync = &storage.MDSync{Action: "recovery"}
	if _, err := o.mdResume(context.Background(), "tank", md); err == nil || err.Code != CodePoolBusy {
		t.Fatalf("recovery running: err = %v", err)
	}

	// The pool's block is gone from mdadm.conf: nothing is offered, nothing done.
	o, run, md = newMDResumeEnv(t)
	o.Files = NewMemFiles()
	if got := o.mdReturned(context.Background(), "tank", md); len(got) != 0 {
		t.Fatalf("returned without mdadm.conf = %v", got)
	}
	if _, err := o.mdResume(context.Background(), "tank", md); err == nil || slices.ContainsFunc(run.Calls(), func(c []string) bool { return c[0] == "mdadm" }) {
		t.Fatalf("without mdadm.conf: err = %v, calls %v", err, run.Calls())
	}
}
