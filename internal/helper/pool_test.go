// SPDX-License-Identifier: AGPL-3.0-or-later

package helper

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/crosswax/nasbuster/internal/fixture"
	"github.com/crosswax/nasbuster/internal/storage"
	"github.com/crosswax/nasbuster/internal/sysexec"
)

const testUUID = "7a31ae09-61e0-414b-8124-68d1d2b89674"

func gib(n uint64) uint64 { return n << 30 }

// poolEnv is a fake system: 4 blank disks (2 USB with by-id, 2 lab loops),
// one disk with data, one system disk and one protected disk.
type poolEnv struct {
	ops        *Ops
	run        *sysexec.Fake
	files      *MemFiles
	failMkfs   bool
	mounted    map[string]bool
	signatures map[string]string // device path -> what blkid -p finds on it
}

// sysBlock is the kernel view (sysfs) of the fake disks: no partitions, no holders.
const sysBlock = `
-- sys/block/sda/size --
-- sys/block/sdb/size --
-- sys/block/sdc/size --
-- sys/block/sdd/size --
-- sys/block/sde/size --
-- sys/block/loop2/size --
-- sys/block/mmcblk0/size --
`

func newPoolEnv(t *testing.T) *poolEnv {
	t.Helper()
	e := &poolEnv{run: sysexec.NewFake(), files: NewMemFiles(), mounted: map[string]bool{}, signatures: map[string]string{}}
	disks := []storage.Disk{
		{ID: "wwn-0xa", ByIDPath: "/dev/disk/by-id/wwn-0xa", DevPath: "/dev/sda", Transport: "usb", SizeBytes: gib(4000), Role: storage.RoleAvailable},
		{ID: "wwn-0xb", ByIDPath: "/dev/disk/by-id/wwn-0xb", DevPath: "/dev/sdb", Transport: "usb", SizeBytes: gib(4000), Role: storage.RoleAvailable},
		{ID: "wwn-0xc", ByIDPath: "/dev/disk/by-id/wwn-0xc", DevPath: "/dev/sdc", Transport: "usb", SizeBytes: gib(4000), Role: storage.RoleAvailable},
		{ID: "lab-loop2", DevPath: "/dev/loop2", Transport: "loop", SizeBytes: gib(1), Role: storage.RoleAvailable},
		{ID: "wwn-0xdata", ByIDPath: "/dev/disk/by-id/wwn-0xdata", DevPath: "/dev/sdd", Transport: "usb", SizeBytes: gib(4000), Role: storage.RoleInUse},
		{ID: "mmc-SD", ByIDPath: "/dev/disk/by-id/mmc-SD", DevPath: "/dev/mmcblk0", Transport: "sd_card", SizeBytes: gib(32), Role: storage.RoleSystem},
		{ID: "wwn-0xhost", ByIDPath: "/dev/disk/by-id/wwn-0xhost", DevPath: "/dev/sde", Transport: "virtual", SizeBytes: gib(256), Role: storage.RoleProtected},
	}
	e.ops = &Ops{
		Run:      e.run,
		FS:       fixture.Parse([]byte(sysBlock)),
		Logger:   quietLogger(),
		Discover: func(context.Context) ([]storage.Disk, error) { return disks, nil },
		Files:    e.files,
		UnitDir:  "/etc/systemd/system",
		MDConf:   "/etc/mdadm/mdadm.conf",
		Sleep:    func(time.Duration) {},
	}
	ok := sysexec.Result{}
	e.run.Handle("sgdisk", func(args []string) (sysexec.Result, error) {
		if args[0] == "-n" { // the kernel exposes the new partition
			dev := args[len(args)-1]
			if strings.HasPrefix(dev, "/dev/loop") {
				e.files.WriteFile(dev+"p1", nil, 0)
			} else {
				e.files.WriteFile(dev+"-part1", nil, 0)
			}
		}
		return ok, nil
	})
	e.run.Handle("udevadm", func([]string) (sysexec.Result, error) { return ok, nil })
	e.run.Handle("wipefs", func([]string) (sysexec.Result, error) { return ok, nil })
	e.run.Handle("mdadm", func(args []string) (sysexec.Result, error) {
		switch args[0] {
		case "--create":
			e.files.WriteFile(args[1], nil, 0)
		case "--detail":
			return sysexec.Result{Stdout: []byte("ARRAY /dev/md/cwnsb-tank metadata=1.2 UUID=753ebdf0:71c967bf:96f38de7:06791455\n")}, nil
		}
		return ok, nil
	})
	e.run.Handle("mkfs.btrfs", func([]string) (sysexec.Result, error) {
		if e.failMkfs {
			return sysexec.Result{ExitCode: 1, Stderr: []byte("ERROR: /dev/sda-part1 appears to contain an existing filesystem (ext4)")}, nil
		}
		return ok, nil
	})
	e.run.Handle("blkid", func(args []string) (sysexec.Result, error) {
		if slices.Equal(args[:3], []string{"-p", "-o", "export"}) {
			dev := args[3]
			// As the real blkid: a partition always reports its table entry (exit 0).
			entry := ""
			if strings.HasSuffix(dev, "-part1") || strings.HasSuffix(dev, "p1") {
				entry = "DEVNAME=" + dev + "\nPART_ENTRY_SCHEME=gpt\nPART_ENTRY_NAME=cwnsb-tank\nPART_ENTRY_NUMBER=1\nPART_ENTRY_OFFSET=2048\n"
			}
			if sig := e.signatures[dev]; sig != "" {
				return sysexec.Result{Stdout: []byte(sig + "\n" + entry)}, nil
			}
			if entry != "" {
				return sysexec.Result{Stdout: []byte(entry)}, nil
			}
			return sysexec.Result{ExitCode: 2}, nil // nothing found: blank
		}
		return sysexec.Result{Stdout: []byte(testUUID + "\n")}, nil
	})
	e.run.Handle("systemctl", func(args []string) (sysexec.Result, error) {
		if len(args) == 3 && args[0] == "enable" {
			e.mounted[storage.MountPoint(strings.TrimSuffix(strings.TrimPrefix(args[2], "srv-cwnsb-"), ".mount"))] = true
		}
		return ok, nil
	})
	e.run.Handle("findmnt", func(args []string) (sysexec.Result, error) {
		mnt := args[len(args)-1]
		if !e.mounted[mnt] {
			return sysexec.Result{ExitCode: 1}, nil
		}
		return sysexec.Result{Stdout: []byte(`{"filesystems":[{"target":"` + mnt + `","source":"/dev/sda1","fstype":"btrfs","uuid":"` + testUUID + `"}]}`)}, nil
	})
	return e
}

func (e *poolEnv) create(a PoolCreateArgs) (PoolCreateResult, *Error) {
	return e.ops.poolCreate(context.Background(), a)
}

// ran returns the command lines run for a program.
func (e *poolEnv) ran(name string) [][]string {
	var out [][]string
	for _, c := range e.run.Calls() {
		if c[0] == name {
			out = append(out, c[1:])
		}
	}
	return out
}

func (e *poolEnv) destructive() int {
	n := 0
	for _, c := range e.run.Calls() {
		switch c[0] {
		case "sgdisk", "mkfs.btrfs", "wipefs", "mdadm":
			n++
		}
	}
	return n
}

func TestPoolCreateMirror(t *testing.T) {
	e := newPoolEnv(t)
	res, err := e.create(PoolCreateArgs{Name: "tank", Layout: storage.LayoutMirror, Disks: []string{"wwn-0xa", "wwn-0xb"}, Confirm: "tank"})
	if err != nil {
		t.Fatal(err)
	}
	if res.UUID != testUUID || res.MountPoint != "/srv/cwnsb/tank" || res.MDDevice != "" || !slices.Equal(res.Members, []string{"wwn-0xa", "wwn-0xb"}) {
		t.Fatalf("result = %+v", res)
	}
	sg := e.ran("sgdisk")
	if len(sg) != 2 || !slices.Equal(sg[0], []string{"-n", "1:0:-100M", "-t", "1:8300", "-c", "1:cwnsb-tank", "/dev/disk/by-id/wwn-0xa"}) {
		t.Fatalf("sgdisk = %v", sg)
	}
	mk := e.ran("mkfs.btrfs")
	if len(mk) != 1 || !slices.Equal(mk[0], []string{"-L", "cwnsb-tank", "-d", "raid1", "-m", "raid1",
		"/dev/disk/by-id/wwn-0xa-part1", "/dev/disk/by-id/wwn-0xb-part1"}) {
		t.Fatalf("mkfs = %v", mk)
	}
	if slices.Contains(mk[0], "-f") {
		t.Fatal("mkfs.btrfs must never force (-f)")
	}
	unit, _ := e.files.ReadFile("/etc/systemd/system/srv-cwnsb-tank.mount")
	if !strings.Contains(string(unit), "What=/dev/disk/by-uuid/"+testUUID) {
		t.Fatalf("unit = %s", unit)
	}
	if len(e.ran("mdadm")) != 0 {
		t.Fatal("mirror must not create an md array")
	}
}

func TestPoolCreateMirrorOfThreeUsesRAID1C3Metadata(t *testing.T) {
	e := newPoolEnv(t)
	if _, err := e.create(PoolCreateArgs{Name: "tank", Layout: storage.LayoutMirror, Disks: []string{"wwn-0xa", "wwn-0xb", "wwn-0xc"}, Confirm: "tank"}); err != nil {
		t.Fatal(err)
	}
	if mk := e.ran("mkfs.btrfs")[0]; !slices.Contains(mk, "raid1c3") {
		t.Fatalf("mkfs = %v", mk)
	}
}

func TestPoolCreateParity(t *testing.T) {
	e := newPoolEnv(t)
	res, err := e.create(PoolCreateArgs{Name: "tank", Layout: storage.LayoutParity1, Disks: []string{"wwn-0xa", "wwn-0xb", "wwn-0xc"}, Confirm: "tank"})
	if err != nil {
		t.Fatal(err)
	}
	if res.MDDevice != "/dev/md/cwnsb-tank" {
		t.Fatalf("md = %q", res.MDDevice)
	}
	create := e.ran("mdadm")[0]
	for _, want := range []string{"--create", "/dev/md/cwnsb-tank", "--level=5", "--raid-devices=3", "--bitmap=internal", "--metadata=1.2"} {
		if !slices.Contains(create, want) {
			t.Errorf("mdadm --create lacks %s: %v", want, create)
		}
	}
	if sg := e.ran("sgdisk"); !slices.Contains(sg[0], "1:FD00") {
		t.Errorf("parity members must be Linux RAID partitions: %v", sg[0])
	}
	if mk := e.ran("mkfs.btrfs")[0]; !slices.Equal(mk, []string{"-L", "cwnsb-tank", "-d", "single", "-m", "dup", "/dev/md/cwnsb-tank"}) {
		t.Errorf("mkfs = %v", mk)
	}
	conf, _ := e.files.ReadFile("/etc/mdadm/mdadm.conf")
	if !strings.Contains(string(conf), "# BEGIN CrossWax NasBuster pool tank\nARRAY /dev/md/cwnsb-tank") {
		t.Errorf("mdadm.conf = %q", conf)
	}
}

func TestPoolCreateLabLoop(t *testing.T) {
	e := newPoolEnv(t)
	if _, err := e.create(PoolCreateArgs{Name: "lab", Layout: storage.LayoutSingle, Disks: []string{"lab-loop2"}, Confirm: "lab"}); err != nil {
		t.Fatal(err)
	}
	if mk := e.ran("mkfs.btrfs")[0]; mk[len(mk)-1] != "/dev/loop2p1" || !slices.Contains(mk, "dup") {
		t.Fatalf("mkfs = %v", mk)
	}
}

func TestPoolCreateRefusesDisksThatAreNotBlank(t *testing.T) {
	for _, id := range []string{"wwn-0xdata", "mmc-SD", "wwn-0xhost"} {
		e := newPoolEnv(t)
		_, err := e.create(PoolCreateArgs{Name: "tank", Layout: storage.LayoutMirror, Disks: []string{"wwn-0xa", id}, Confirm: "tank"})
		if err == nil || err.Code != CodeDiskNotAvailable {
			t.Errorf("%s: err = %v, want %s", id, err, CodeDiskNotAvailable)
		}
		if n := e.destructive(); n != 0 {
			t.Errorf("%s: %d destructive commands ran before refusing", id, n)
		}
	}
}

// The udev database can lag behind a write: discovery says "available" but the
// disk was just formatted. The helper's own low-level probe must catch it.
func TestPoolCreateRefusesStaleBlankDisk(t *testing.T) {
	e := newPoolEnv(t)
	e.signatures["/dev/disk/by-id/wwn-0xb"] = "UUID=1234\nTYPE=ext4"
	_, err := e.create(PoolCreateArgs{Name: "tank", Layout: storage.LayoutMirror, Disks: []string{"wwn-0xa", "wwn-0xb"}, Confirm: "tank"})
	if err == nil || err.Code != CodeDiskNotAvailable || !strings.Contains(err.Message, "ext4") {
		t.Fatalf("err = %v", err)
	}
	if e.destructive() != 0 {
		t.Fatal("destructive commands ran on a disk with data")
	}
}

// A disk whose partition table was removed by another tool still holds its old
// file system: invisible on the disk, it reappears inside the new partition.
// NasBuster refuses before formatting and leaves that file system where it is.
func TestPoolCreateRefusesResidualFileSystem(t *testing.T) {
	e := newPoolEnv(t)
	e.signatures["/dev/disk/by-id/wwn-0xb-part1"] = "UUID=1234\nTYPE=ntfs"
	_, err := e.create(PoolCreateArgs{Name: "tank", Layout: storage.LayoutMirror, Disks: []string{"wwn-0xa", "wwn-0xb"}, Confirm: "tank"})
	if err == nil || err.Code != CodeDiskResidual {
		t.Fatalf("err = %v, want %s", err, CodeDiskResidual)
	}
	if err.Params["disk"] != "wwn-0xb" || err.Params["found"] != "ntfs" {
		t.Errorf("params = %v", err.Params)
	}
	if n := len(e.ran("mkfs.btrfs")) + len(e.ran("mdadm")); n != 0 {
		t.Errorf("%d format commands ran on a disk with old data", n)
	}
	if w := e.ran("wipefs"); len(w) != 0 {
		t.Errorf("the old file system must stay: wipefs = %v", w)
	}
	var zapped []string
	for _, a := range e.ran("sgdisk") {
		if a[0] == "--zap-all" {
			zapped = append(zapped, a[1])
		}
	}
	if !slices.Equal(zapped, []string{"/dev/disk/by-id/wwn-0xa", "/dev/disk/by-id/wwn-0xb"}) {
		t.Errorf("our partition tables must be removed: zapped = %v", zapped)
	}
}

// Outputs of blkid -p -o export seen in the lab (Debian 13, util-linux 2.41).
func TestProbeSignature(t *testing.T) {
	const entry = "DEVNAME=/dev/loop0p1\nPART_ENTRY_SCHEME=gpt\nPART_ENTRY_NAME=cwnsb-x\nPART_ENTRY_NUMBER=1\nPART_ENTRY_OFFSET=2048\n"
	cases := []struct {
		name   string
		res    sysexec.Result
		want   string
		failed bool
	}{
		{"nothing", sysexec.Result{ExitCode: 2}, "", false},
		{"empty partition: its table entry only", sysexec.Result{Stdout: []byte(entry)}, "", false},
		{"file system", sysexec.Result{Stdout: []byte("DEVNAME=/dev/loop0p1\nUUID=3d75\nTYPE=ext4\nUSAGE=filesystem\n" + entry[21:])}, "ext4", false},
		{"partition table", sysexec.Result{Stdout: []byte("DEVNAME=/dev/sda\nPTUUID=1094\nPTTYPE=dos\n")}, "dos", false},
		{"ambiguous", sysexec.Result{ExitCode: 8, Stdout: []byte(entry)}, "unknown", false},
		{"unnamed content", sysexec.Result{Stdout: []byte("DEVNAME=/dev/loop0p1\nUSAGE=other\n")}, "unknown", false},
		{"probe error", sysexec.Result{ExitCode: 4}, "", true},
	}
	for _, c := range cases {
		e := newPoolEnv(t)
		e.run.Handle("blkid", func([]string) (sysexec.Result, error) { return c.res, nil })
		got, err := e.ops.probeSignature(context.Background(), "/dev/loop0p1")
		if got != c.want || (err != nil) != c.failed {
			t.Errorf("%s: got %q, %v", c.name, got, err)
		}
	}
}

func TestPoolCreateResidualProbeFailsClosed(t *testing.T) {
	e := newPoolEnv(t)
	e.run.Handle("blkid", func(args []string) (sysexec.Result, error) {
		if strings.HasSuffix(args[len(args)-1], "-part1") {
			return sysexec.Result{ExitCode: 4}, nil // probe error, not "nothing found"
		}
		return sysexec.Result{ExitCode: 2}, nil
	})
	_, err := e.create(PoolCreateArgs{Name: "tank", Layout: storage.LayoutSingle, Disks: []string{"wwn-0xa"}, Confirm: "tank"})
	if err == nil || err.Code != CodeFailed {
		t.Fatalf("err = %v", err)
	}
	if len(e.ran("mkfs.btrfs")) != 0 || len(e.ran("wipefs")) != 0 {
		t.Error("nothing may be formatted or wiped on a probe that cannot answer")
	}
}

func TestPoolCreateRefusesKernelPartitionsAndHolders(t *testing.T) {
	for _, extra := range []string{"-- sys/block/sdb/sdb1/size --\n", "-- sys/block/sdb/holders/md127 --\n"} {
		e := newPoolEnv(t)
		e.ops.FS = fixture.Parse([]byte(sysBlock + extra))
		_, err := e.create(PoolCreateArgs{Name: "tank", Layout: storage.LayoutMirror, Disks: []string{"wwn-0xa", "wwn-0xb"}, Confirm: "tank"})
		if err == nil || err.Code != CodeDiskNotAvailable || e.destructive() != 0 {
			t.Errorf("%q: err = %v, destructive = %d", extra, err, e.destructive())
		}
	}
}

func TestPoolCreateFailsClosedWhenProbeFails(t *testing.T) {
	e := newPoolEnv(t)
	e.ops.FS = fixture.Parse(nil) // sysfs unreadable: cannot verify
	_, err := e.create(PoolCreateArgs{Name: "tank", Layout: storage.LayoutMirror, Disks: []string{"wwn-0xa", "wwn-0xb"}, Confirm: "tank"})
	if err == nil || err.Code != CodeFailed || e.destructive() != 0 {
		t.Fatalf("err = %v, destructive = %d", err, e.destructive())
	}
}

func TestPoolCreateValidation(t *testing.T) {
	cases := []struct {
		args PoolCreateArgs
		code string
	}{
		{PoolCreateArgs{Name: "my-pool", Layout: "mirror", Disks: []string{"wwn-0xa", "wwn-0xb"}, Confirm: "my-pool"}, CodePoolName},
		{PoolCreateArgs{Name: "tank", Layout: "raid5", Disks: []string{"wwn-0xa", "wwn-0xb", "wwn-0xc"}, Confirm: "tank"}, CodeLayout},
		{PoolCreateArgs{Name: "tank", Layout: "mirror", Disks: []string{"wwn-0xa", "wwn-0xb"}, Confirm: "tanks"}, CodeConfirm},
		{PoolCreateArgs{Name: "tank", Layout: "mirror", Disks: []string{"wwn-0xa"}, Confirm: "tank"}, CodeDiskCount},
		{PoolCreateArgs{Name: "tank", Layout: "single", Disks: []string{"wwn-0xa", "wwn-0xb"}, Confirm: "tank"}, CodeDiskCount},
		{PoolCreateArgs{Name: "tank", Layout: "mirror", Disks: []string{"wwn-0xa", "wwn-0xa"}, Confirm: "tank"}, CodeInvalidArgs},
		{PoolCreateArgs{Name: "tank", Layout: "mirror", Disks: []string{"wwn-0xa", "../sda"}, Confirm: "tank"}, CodeInvalidArgs},
		{PoolCreateArgs{Name: "tank", Layout: "mirror", Disks: []string{"wwn-0xa", "wwn-0xnope"}, Confirm: "tank"}, CodeDiskNotFound},
		{PoolCreateArgs{Name: "tank", Layout: "mirror", Disks: []string{"wwn-0xa", "lab-loop2"}, Confirm: "tank"}, ""}, // allowed: sizes differ but both blank
	}
	for _, c := range cases {
		e := newPoolEnv(t)
		_, err := e.create(c.args)
		got := ""
		if err != nil {
			got = err.Code
		}
		if got != c.code {
			t.Errorf("%+v: code = %q, want %q (%v)", c.args, got, c.code, err)
		}
		if c.code != "" && e.destructive() != 0 {
			t.Errorf("%+v: destructive commands ran before refusing", c.args)
		}
	}
}

func TestPoolCreateRefusesExistingPool(t *testing.T) {
	e := newPoolEnv(t)
	e.files.WriteFile("/etc/systemd/system/srv-cwnsb-tank.mount", []byte("x"), 0)
	_, err := e.create(PoolCreateArgs{Name: "tank", Layout: storage.LayoutMirror, Disks: []string{"wwn-0xa", "wwn-0xb"}, Confirm: "tank"})
	if err == nil || err.Code != CodePoolExists || e.destructive() != 0 {
		t.Fatalf("err = %v, destructive = %d", err, e.destructive())
	}
}

func TestPoolCreateRollsBackOnFailure(t *testing.T) {
	e := newPoolEnv(t)
	e.failMkfs = true
	_, err := e.create(PoolCreateArgs{Name: "tank", Layout: storage.LayoutParity1, Disks: []string{"wwn-0xa", "wwn-0xb", "wwn-0xc"}, Confirm: "tank"})
	if err == nil || err.Code != CodeFailed {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Message, "existing filesystem") {
		t.Errorf("message should carry mkfs's reason: %s", err.Message)
	}
	// md stopped, every new partition wiped and every table zapped — on the
	// three validated disks only.
	if md := e.ran("mdadm"); !slices.ContainsFunc(md, func(a []string) bool { return a[0] == "--stop" && a[1] == "/dev/md/cwnsb-tank" }) {
		t.Errorf("md array not stopped: %v", md)
	}
	var zapped []string
	for _, a := range e.ran("sgdisk") {
		if a[0] == "--zap-all" {
			zapped = append(zapped, a[1])
		}
	}
	if !slices.Equal(zapped, []string{"/dev/disk/by-id/wwn-0xa", "/dev/disk/by-id/wwn-0xb", "/dev/disk/by-id/wwn-0xc"}) {
		t.Errorf("zapped = %v", zapped)
	}
	if len(e.ran("wipefs")) != 3 {
		t.Errorf("wipefs = %v", e.ran("wipefs"))
	}
	if e.files.Exists("/etc/systemd/system/srv-cwnsb-tank.mount") || len(e.ran("systemctl")) != 0 {
		t.Error("nothing should have been mounted")
	}
}

func TestPoolCreateRollbackRemovesUnitAndMDConf(t *testing.T) {
	e := newPoolEnv(t)
	e.files.WriteFile("/etc/mdadm/mdadm.conf", []byte("HOMEHOST <system>\n"), 0)
	// Mount verification fails: everything up to the unit must be undone.
	e.run.Handle("findmnt", func([]string) (sysexec.Result, error) { return sysexec.Result{ExitCode: 1}, nil })
	_, err := e.create(PoolCreateArgs{Name: "tank", Layout: storage.LayoutParity1, Disks: []string{"wwn-0xa", "wwn-0xb", "wwn-0xc"}, Confirm: "tank"})
	if err == nil {
		t.Fatal("expected failure")
	}
	if e.files.Exists("/etc/systemd/system/srv-cwnsb-tank.mount") || e.files.Exists("/srv/cwnsb/tank") {
		t.Errorf("unit or mount point left behind: %v", e.files.Paths())
	}
	if conf, _ := e.files.ReadFile("/etc/mdadm/mdadm.conf"); string(conf) != "HOMEHOST <system>\n" {
		t.Errorf("mdadm.conf not restored: %q", conf)
	}
	if sc := e.ran("systemctl"); !slices.ContainsFunc(sc, func(a []string) bool { return a[0] == "disable" }) {
		t.Errorf("unit not disabled: %v", sc)
	}
}

func TestPoolStatusAndScrub(t *testing.T) {
	b, err := os.ReadFile("../storage/testdata/btrfs.txtar")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]byte{}
	for _, s := range fixture.Sections(b) {
		out[s.Name] = s.Data
	}
	e := newPoolEnv(t)
	e.ops.FS = fixture.Parse([]byte("-- dev/md/cwnsb-tank -> ../md127 --\n-- proc/mdstat --\n" + string(out["mdstat.txt"])))
	mnt := "/srv/cwnsb/tank"
	e.run.Set(sysexec.Result{Stdout: out["device-stats.json"]}, "btrfs", "--format", "json", "device", "stats", mnt)
	e.run.Set(sysexec.Result{Stdout: out["filesystem-df.json"]}, "btrfs", "--format", "json", "filesystem", "df", mnt)
	e.run.Set(sysexec.Result{Stdout: out["filesystem-usage.txt"]}, "btrfs", "filesystem", "usage", "-b", mnt)
	e.run.Set(sysexec.Result{Stdout: out["scrub-status.txt"]}, "btrfs", "scrub", "status", "-R", mnt)
	fakeUnits(e.run, "activating")

	st, opErr := e.ops.poolStatus(context.Background(), PoolArgs{Name: "tank"})
	if opErr != nil || st.Mounted || st.Health != storage.HealthUnmounted {
		t.Fatalf("unmounted: %+v %v", st, opErr)
	}
	if _, opErr := e.ops.poolScrub(context.Background(), PoolArgs{Name: "tank"}); opErr == nil || opErr.Code != CodePoolNotMounted {
		t.Fatalf("scrub unmounted: %v", opErr)
	}

	e.mounted[mnt] = true
	st, opErr = e.ops.poolStatus(context.Background(), PoolArgs{Name: "tank"})
	if opErr != nil || !st.Mounted || st.UUID != testUUID || len(st.Devices) != 2 || st.FreeBytes != 379498496 || st.Scrub.Status != "finished" {
		t.Fatalf("status = %+v %v", st, opErr)
	}
	if st.MD == nil || st.MD.Level != "raid1" || st.MD.Sync == nil || st.MD.Device != "/dev/md/cwnsb-tank" {
		t.Fatalf("md = %+v", st.MD)
	}
	if st.Health != storage.HealthErrors { // the fixture has device errors
		t.Fatalf("health = %s", st.Health)
	}
	if _, opErr := e.ops.poolScrub(context.Background(), PoolArgs{Name: "tank"}); opErr != nil {
		t.Fatalf("scrub: %v", opErr)
	}
}

// During a parity repair (ADR-0032), /proc/mdstat shows "resync": only sysfs
// says "repair".
func TestMDStatusTellsRepairFromResync(t *testing.T) {
	const mdstat = "Personalities : [raid5]\n" +
		"md127 : active raid5 sda1[0] sdd1[3] sdc1[1]\n" +
		"      243063808 blocks super 1.2 level 5, 512k chunk, algorithm 2 [3/3] [UUU]\n" +
		"      [=>...................]  resync =  8.6% (10485760/121531904) finish=9.2min speed=200000K/sec\n" +
		"      bitmap: 0/1 pages [0KB], 65536KB chunk\n\nunused devices: <none>\n"
	for _, tc := range []struct{ action, want string }{{"repair\n", "repair"}, {"resync\n", "resync"}, {"", "resync"}} {
		files := "-- dev/md/cwnsb-e2 -> ../md127 --\n-- proc/mdstat --\n" + mdstat
		if tc.action != "" {
			files += "-- sys/block/md127/md/sync_action --\n" + tc.action
		}
		o := &Ops{FS: fixture.Parse([]byte(files))}
		md := o.mdStatus("e2")
		if md == nil || md.Sync == nil || md.Sync.Action != tc.want || md.Sync.Percent != 8.6 {
			t.Fatalf("sync_action %q: md = %+v", tc.action, md)
		}
	}
}
