// SPDX-License-Identifier: AGPL-3.0-or-later

package storage_test

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/crosswax/nasbuster/internal/demo"
	"github.com/crosswax/nasbuster/internal/fixture"
	"github.com/crosswax/nasbuster/internal/storage"
	"github.com/crosswax/nasbuster/internal/sysexec"
)

func demoDisks(t *testing.T) map[string]storage.Disk {
	t.Helper()
	sys := demo.New()
	d := &storage.Discoverer{Run: sys.Runner, FS: sys.FS}
	disks, err := d.Disks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byDev := map[string]storage.Disk{}
	for _, disk := range disks {
		byDev[disk.DevPath] = disk
	}
	return byDev
}

func TestDiscoverDemoPi(t *testing.T) {
	disks := demoDisks(t)
	if len(disks) != 5 {
		t.Fatalf("got %d disks, want 5 (zram must be ignored): %v", len(disks), disks)
	}

	sda := disks["/dev/sda"]
	if sda.ID != "wwn-0x5000c500c1234567" || sda.Role != storage.RoleAvailable || sda.Transport != "usb" {
		t.Errorf("sda = %+v", sda)
	}
	if sda.USB == nil || sda.USB.Driver != "uas" || sda.USB.VendorID != "174c" || len(sda.Warnings) != 0 {
		t.Errorf("sda usb/warnings = %+v %v", sda.USB, sda.Warnings)
	}
	if sda.Vendor != "Seagate" || sda.ByIDPath != "/dev/disk/by-id/wwn-0x5000c500c1234567" {
		t.Errorf("sda vendor/byId = %q %q", sda.Vendor, sda.ByIDPath)
	}

	sdb := disks["/dev/sdb"]
	if sdb.Role != storage.RoleInUse || sdb.ID != "usb-WDC_WD40EFRX-68N32N0_0123456789ABCDEF-0:0" {
		t.Errorf("sdb = %s %s", sdb.Role, sdb.ID)
	}
	if !slices.Contains(sdb.Warnings, storage.WarnDuplicateSerial) || !slices.Contains(sdb.Warnings, storage.WarnUSBStorageDriver) {
		t.Errorf("sdb warnings = %v", sdb.Warnings)
	}
	kinds := []string{}
	for _, u := range sdb.Usage {
		kinds = append(kinds, u.Kind)
	}
	if !slices.Equal(kinds, []string{"partition_table", "filesystem", "mounted"}) {
		t.Errorf("sdb usage = %+v", sdb.Usage)
	}
	if len(sdb.Partitions) != 1 || sdb.Partitions[0].FSType != "ext4" || sdb.Partitions[0].Label != "data" {
		t.Errorf("sdb partitions = %+v", sdb.Partitions)
	}

	sdc := disks["/dev/sdc"]
	if sdc.Role != storage.RoleAvailable || !slices.Contains(sdc.Warnings, storage.WarnDuplicateSerial) {
		t.Errorf("sdc = %s %v", sdc.Role, sdc.Warnings)
	}

	sd := disks["/dev/mmcblk0"]
	if sd.Role != storage.RoleSystem || sd.Transport != "sd_card" || sd.ID != "mmc-SN32G_0x1234abcd" || sd.Model != "SN32G" {
		t.Errorf("mmcblk0 = %s %s %s %q", sd.Role, sd.Transport, sd.ID, sd.Model)
	}
}

func TestDiskLookup(t *testing.T) {
	sys := demo.New()
	d := &storage.Discoverer{Run: sys.Runner, FS: sys.FS}
	if _, err := d.Disk(context.Background(), "wwn-0x5000c500c1234567"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Disk(context.Background(), "nope"); err != storage.ErrNotFound {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// labLsblk mimics the WSL lab: Hyper-V virtual disks (one of them the lab's own
// root, others belonging to OTHER distros and Docker), a read-only Docker ISO
// loop, and two loop devices: one inside the lab dir, one outside.
const labLsblk = `{"blockdevices":[
 {"name":"loop0","kname":"loop0","path":"/dev/loop0","size":807403520,"type":"loop","tran":null,"rota":true,"rm":false,"ro":true,"fstype":"iso9660","pttype":null,"mountpoints":["/mnt/wsl/docker-desktop/cli-tools"]},
 {"name":"loop2","kname":"loop2","path":"/dev/loop2","size":1073741824,"type":"loop","tran":null,"rota":true,"rm":false,"ro":false,"fstype":null,"pttype":null,"mountpoints":[]},
 {"name":"loop3","kname":"loop3","path":"/dev/loop3","size":1073741824,"type":"loop","tran":null,"rota":true,"rm":false,"ro":false,"fstype":null,"pttype":null,"mountpoints":[]},
 {"name":"sdc","kname":"sdc","path":"/dev/sdc","size":1099511627776,"type":"disk","tran":null,"rota":true,"rm":false,"ro":false,"model":"Virtual Disk","vendor":"Msft    ","fstype":"ext4","pttype":null,"mountpoints":[null]},
 {"name":"sdf","kname":"sdf","path":"/dev/sdf","size":274877906944,"type":"disk","tran":null,"rota":true,"rm":false,"ro":false,"model":"Virtual Disk","vendor":"Msft    ","fstype":"ext4","pttype":null,"mountpoints":["/"]}
]}`

func TestLabModeProtectsEverythingElse(t *testing.T) {
	run := sysexec.NewFake().Set(sysexec.Result{Stdout: []byte(labLsblk)}, "lsblk", storage.LsblkArgs...)
	run.Set(sysexec.Result{Stdout: []byte("ID_BUS=scsi\nID_VENDOR=Msft\nDEVLINKS=/dev/disk/by-id/wwn-0x600224807330d59afbb259348928723a\n")}, "udevadm", storage.UdevArgs("/dev/sdc")...)
	run.Set(sysexec.Result{Stdout: []byte("ID_BUS=scsi\nID_VENDOR=Msft\nDEVLINKS=/dev/disk/by-id/scsi-360022480c5a14d0f62c143adf69a1a2d\n")}, "udevadm", storage.UdevArgs("/dev/sdf")...)
	root := fixture.Parse([]byte(`
-- sys/block/loop0/loop/backing_file --
/mnt/docker-desktop-disk/isocache/entries/docker-wsl-cli.iso/aaf3
-- sys/block/loop2/loop/backing_file --
/var/lib/cwnsb-lab/disk1.img
-- sys/block/loop3/loop/backing_file --
/var/lib/cwnsb-lab-evil/disk2.img
`))
	d := &storage.Discoverer{Run: run, FS: root, LabDir: "/var/lib/cwnsb-lab"}
	disks, err := d.Disks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	roles := map[string]string{}
	for _, disk := range disks {
		roles[disk.DevPath] = disk.Role
		if disk.DevPath == "/dev/loop2" && (disk.ID != "lab-loop2" || disk.Transport != "loop") {
			t.Errorf("loop2 = %+v", disk)
		}
		if disk.DevPath == "/dev/sdc" && disk.Transport != "virtual" {
			t.Errorf("sdc transport = %s", disk.Transport)
		}
	}
	want := map[string]string{
		"/dev/loop0": storage.RoleProtected, // Docker ISO, outside the lab
		"/dev/loop2": storage.RoleAvailable, // the only lab disk
		"/dev/loop3": storage.RoleProtected, // prefix trick: cwnsb-lab-evil is NOT inside cwnsb-lab
		"/dev/sdc":   storage.RoleProtected, // another distro's virtual disk, unmounted here
		"/dev/sdf":   storage.RoleProtected, // the lab's own root
	}
	for dev, role := range want {
		if roles[dev] != role {
			t.Errorf("%s role = %q, want %q", dev, roles[dev], role)
		}
	}
}

func TestLoopsIgnoredOutsideLab(t *testing.T) {
	run := sysexec.NewFake().Set(sysexec.Result{Stdout: []byte(labLsblk)}, "lsblk", storage.LsblkArgs...)
	d := &storage.Discoverer{Run: run, FS: fixture.Parse(nil)}
	disks, err := d.Disks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, disk := range disks {
		if disk.Transport == "loop" {
			t.Errorf("loop device listed outside lab mode: %s", disk.DevPath)
		}
		if disk.DevPath == "/dev/sdc" && (disk.Role != storage.RoleInUse || disk.ID != "dev-sdc") {
			// No udev data → no stable id → never offered as available.
			t.Errorf("sdc without stable id = %s %s", disk.Role, disk.ID)
		}
	}
}

func TestOldLsblkStringTypes(t *testing.T) {
	old := `{"blockdevices":[{"name":"sda","kname":"sda","path":"/dev/sda","size":"500107862016","type":"disk","tran":"sata","rota":"1","rm":"0","ro":"0","serial":"S1","mountpoint":null}]}`
	run := sysexec.NewFake().Set(sysexec.Result{Stdout: []byte(old)}, "lsblk", storage.LsblkArgs...)
	run.Set(sysexec.Result{Stdout: []byte("DEVLINKS=/dev/disk/by-id/ata-DISK_S1\n")}, "udevadm", storage.UdevArgs("/dev/sda")...)
	disks, err := (&storage.Discoverer{Run: run, FS: fixture.Parse(nil)}).Disks(context.Background())
	if err != nil || len(disks) != 1 {
		t.Fatalf("disks = %v, %v", disks, err)
	}
	if d := disks[0]; d.SizeBytes != 500107862016 || !d.Rotational || d.Transport != "sata" || d.Role != storage.RoleAvailable {
		t.Errorf("disk = %+v", d)
	}
}

func TestDiskLookupBypassesStaleCache(t *testing.T) {
	sys := demo.New()
	d := &storage.Discoverer{Run: sys.Runner, FS: sys.FS, CacheFor: time.Hour}
	ctx := context.Background()
	if _, err := d.Disks(ctx); err != nil { // fill the cache
		t.Fatal(err)
	}
	// A disk the cache cannot know about is looked up again, not reported missing.
	calls := len(sys.Runner.Calls())
	if _, err := d.Disk(ctx, "wwn-0xdeadbeef"); err != storage.ErrNotFound {
		t.Fatalf("err = %v", err)
	}
	if len(sys.Runner.Calls()) == calls {
		t.Fatal("a miss must refresh the cache")
	}
	if _, err := d.Disk(ctx, "wwn-0x5000c500c1234567"); err != nil {
		t.Fatal(err)
	}
}

// TestDiscoverRealPi5 uses lsblk and udevadm outputs captured on the Raspberry
// Pi 5 test unit (testdata/pi5-sd.txtar). Raspberry Pi OS 13 swaps on zram0,
// backed by loop0 (/var/swap): neither may ever be offered for a pool.
func TestDiscoverRealPi5(t *testing.T) {
	data, err := os.ReadFile("testdata/pi5-sd.txtar")
	if err != nil {
		t.Fatal(err)
	}
	run := sysexec.NewFake()
	for _, s := range fixture.Sections(data) {
		switch {
		case s.Name == "lsblk.json":
			run.Set(sysexec.Result{Stdout: s.Data}, "lsblk", storage.LsblkArgs...)
		case strings.HasPrefix(s.Name, "udevadm "):
			run.Set(sysexec.Result{Stdout: s.Data}, "udevadm", storage.UdevArgs(strings.TrimPrefix(s.Name, "udevadm "))...)
		}
	}
	disks, err := (&storage.Discoverer{Run: run, FS: fixture.Parse(nil)}).Disks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(disks) != 1 {
		t.Fatalf("got %d disks, want only the SD card (zram0 and loop0 are swap): %+v", len(disks), disks)
	}
	sd := disks[0]
	if sd.DevPath != "/dev/mmcblk0" || sd.Role != storage.RoleSystem || sd.Transport != "sd_card" ||
		sd.ID != "mmc-USD00_0x00c0ffee" || sd.ByIDPath != "/dev/disk/by-id/mmc-USD00_0x00c0ffee" {
		t.Errorf("SD card = %s role=%s transport=%s id=%s", sd.DevPath, sd.Role, sd.Transport, sd.ID)
	}
	if sd.SizeBytes < 15e9 || sd.SizeBytes > 17e9 {
		t.Errorf("SD card size = %d, want about 16 GB", sd.SizeBytes)
	}
	if len(sd.Partitions) != 2 {
		t.Errorf("partitions = %+v", sd.Partitions)
	}
}

// TestDuplicateByIDFailsClosed: two sticks with the same serial share one
// /dev/disk/by-id name, whose link points at only one of them. Neither may be
// addressed by it, nor offered for a pool; a stick with its own name still is.
func TestDuplicateByIDFailsClosed(t *testing.T) {
	data, err := os.ReadFile("testdata/usb-twin-sticks.txtar")
	if err != nil {
		t.Fatal(err)
	}
	run := sysexec.NewFake()
	for _, s := range fixture.Sections(data) {
		switch {
		case s.Name == "lsblk.json":
			run.Set(sysexec.Result{Stdout: s.Data}, "lsblk", storage.LsblkArgs...)
		case strings.HasPrefix(s.Name, "udevadm "):
			run.Set(sysexec.Result{Stdout: s.Data}, "udevadm", storage.UdevArgs(strings.TrimPrefix(s.Name, "udevadm "))...)
		}
	}
	d := &storage.Discoverer{Run: run, FS: fixture.Parse(nil)}
	disks, err := d.Disks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(disks) != 3 {
		t.Fatalf("got %d disks, want 3: %+v", len(disks), disks)
	}
	const shared = "usb-Generic_Flash_Disk_FBA81234-0:0"
	for i, want := range []string{"dev-sda", "dev-sdb"} {
		disk := disks[i]
		if disk.ID != want || disk.ByIDPath != "" || disk.Role != storage.RoleInUse {
			t.Errorf("%s: id=%q byId=%q role=%s, want %s, no by-id path, in_use", disk.DevPath, disk.ID, disk.ByIDPath, disk.Role, want)
		}
		if !slices.Contains(disk.Usage, storage.Usage{Kind: "duplicate_id", Detail: shared}) {
			t.Errorf("%s usage = %+v, want duplicate_id", disk.DevPath, disk.Usage)
		}
		if !slices.Contains(disk.Warnings, storage.WarnDuplicateSerial) {
			t.Errorf("%s warnings = %v", disk.DevPath, disk.Warnings)
		}
	}
	if sdc := disks[2]; sdc.ID != "usb-Generic_Flash_Disk_FBA85678-0:0" || sdc.Role != storage.RoleAvailable ||
		sdc.ByIDPath != "/dev/disk/by-id/usb-Generic_Flash_Disk_FBA85678-0:0" {
		t.Errorf("sdc = id=%q role=%s byId=%q, want its own id, available", sdc.ID, sdc.Role, sdc.ByIDPath)
	}
	if _, err := d.Disk(context.Background(), shared); err != storage.ErrNotFound {
		t.Errorf("lookup by the shared id: err = %v, want ErrNotFound", err)
	}
}

func TestUSB2LinkWarning(t *testing.T) {
	data, err := os.ReadFile("testdata/usb-twin-sticks.txtar")
	if err != nil {
		t.Fatal(err)
	}
	run := sysexec.NewFake()
	for _, s := range fixture.Sections(data) {
		switch {
		case s.Name == "lsblk.json":
			run.Set(sysexec.Result{Stdout: s.Data}, "lsblk", storage.LsblkArgs...)
		case strings.HasPrefix(s.Name, "udevadm "):
			run.Set(sysexec.Result{Stdout: s.Data}, "udevadm", storage.UdevArgs(strings.TrimPrefix(s.Name, "udevadm "))...)
		}
	}
	// sysfs speed of each stick's USB device (2-1, 2-3), as on the test Pi 5:
	// one in a blue port, one in a black one.
	usb := "sys/devices/platform/axi/1000120000.pcie/1f00200000.usb/xhci-hcd.0/usb2/"
	d := &storage.Discoverer{Run: run, FS: fixture.Parse([]byte("-- " + usb + "2-1/speed --\n5000\n-- " + usb + "2-3/speed --\n480\n"))}
	disks, err := d.Disks(context.Background())
	if err != nil || len(disks) != 3 {
		t.Fatalf("disks = %+v %v", disks, err)
	}
	if sda := disks[0]; sda.USB == nil || sda.USB.SpeedMbps != 5000 || slices.Contains(sda.Warnings, storage.WarnUSB2Link) {
		t.Errorf("sda (USB 3) = %+v %v", sda.USB, sda.Warnings)
	}
	if sdc := disks[2]; sdc.USB == nil || sdc.USB.SpeedMbps != 480 || !slices.Contains(sdc.Warnings, storage.WarnUSB2Link) {
		t.Errorf("sdc (USB 2 link) = %+v %v", sdc.USB, sdc.Warnings)
	}
	if sdb := disks[1]; sdb.USB == nil || sdb.USB.SpeedMbps != 0 || slices.Contains(sdb.Warnings, storage.WarnUSB2Link) {
		t.Errorf("sdb (speed unknown: no warning) = %+v %v", sdb.USB, sdb.Warnings)
	}
}
