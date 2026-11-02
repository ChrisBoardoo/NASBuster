// SPDX-License-Identifier: AGPL-3.0-or-later

package storage

import (
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/crosswax/nasbuster/internal/fixture"
)

const (
	tb = 1000 * 1000 * 1000 * 1000
	gb = 1000 * 1000 * 1000
)

func TestPartitionBytesMatchesLab(t *testing.T) {
	// Measured in cwnsb-lab: sgdisk -n 1:0:-100M on a 512 MiB loop.
	if got := PartitionBytes(536870912); got != 430947840 {
		t.Fatalf("PartitionBytes(512 MiB) = %d, want 430947840", got)
	}
}

func TestPlanPool(t *testing.T) {
	p4 := PartitionBytes(4 * tb)
	p2 := PartitionBytes(2 * tb)
	cases := []struct {
		layout   string
		disks    []uint64
		usable   uint64
		warnings []string
	}{
		{LayoutSingle, []uint64{4 * tb}, p4, []string{PlanNoRedundancy}},
		{LayoutMirror, []uint64{4 * tb, 4 * tb}, p4, []string{}},
		// 4+2+2: total/2 = 4, total-largest = 4 → 4 TB usable.
		{LayoutMirror, []uint64{4 * tb, 2 * tb, 2 * tb}, min((p4+2*p2)/2, 2*p2), []string{PlanMixedSizes}},
		// 4+2: the 4 TB disk can only mirror 2 TB.
		{LayoutMirror, []uint64{4 * tb, 2 * tb}, p2, []string{PlanMixedSizes}},
		{LayoutRAID10, []uint64{4 * tb, 4 * tb, 4 * tb, 4 * tb}, 2 * p4, []string{}},
		{LayoutParity1, []uint64{4 * tb, 4 * tb, 4 * tb}, 2 * p4, []string{PlanDetectOnly}},
		{LayoutParity1, []uint64{4 * tb, 4 * tb, 2 * tb}, 2 * p2, []string{PlanDetectOnly, PlanMixedSizes}},
		{LayoutParity2, []uint64{4 * tb, 4 * tb, 4 * tb, 4 * tb}, 2 * p4, []string{PlanDetectOnly}},
	}
	for _, c := range cases {
		p, err := PlanPool(c.layout, c.disks)
		if err != nil {
			t.Fatalf("%s %v: %v", c.layout, c.disks, err)
		}
		if p.UsableBytes != c.usable || !slices.Equal(p.Warnings, c.warnings) {
			t.Errorf("%s %v = %d %v, want %d %v", c.layout, c.disks, p.UsableBytes, p.Warnings, c.usable, c.warnings)
		}
	}
}

func TestPlanPoolErrors(t *testing.T) {
	if _, err := PlanPool("raid5", []uint64{tb, tb, tb}); !errors.Is(err, ErrUnknownLayout) {
		t.Errorf("btrfs raid5 must not exist: %v", err)
	}
	for layout, n := range map[string]int{LayoutSingle: 2, LayoutMirror: 1, LayoutRAID10: 3, LayoutParity1: 2, LayoutParity2: 3} {
		disks := slices.Repeat([]uint64{tb}, n)
		if _, err := PlanPool(layout, disks); !errors.Is(err, ErrDiskCount) {
			t.Errorf("%s with %d disks: %v", layout, n, err)
		}
	}
	if _, err := PlanPool(LayoutMirror, []uint64{tb, 100 << 20}); !errors.Is(err, ErrDiskTooSmall) {
		t.Errorf("tiny disk accepted")
	}
}

func TestLayoutProfiles(t *testing.T) {
	mirror, _ := LayoutByName(LayoutMirror)
	if d, m := mirror.BtrfsProfiles(2); d != "raid1" || m != "raid1" {
		t.Errorf("mirror/2 = %s/%s", d, m)
	}
	if d, m := mirror.BtrfsProfiles(3); d != "raid1" || m != "raid1c3" {
		t.Errorf("mirror/3 = %s/%s", d, m)
	}
	parity, _ := LayoutByName(LayoutParity1)
	if d, m := parity.BtrfsProfiles(3); d != "single" || m != "dup" || parity.PartitionType() != "FD00" || parity.MDLevel != 5 {
		t.Errorf("parity1 = %s/%s %s", d, m, parity.PartitionType())
	}
}

func TestPoolNames(t *testing.T) {
	for _, ok := range []string{"tank", "data2", "nas"} {
		if !ValidPoolName(ok) {
			t.Errorf("%q rejected", ok)
		}
	}
	for _, bad := range []string{"", "a", "Tank", "my-pool", "2data", "data/x", "../etc", strings.Repeat("a", 25)} {
		if ValidPoolName(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
	if MountUnitName("tank") != "srv-cwnsb-tank.mount" || MountPoint("tank") != "/srv/cwnsb/tank" {
		t.Error("naming")
	}
	unit := MountUnit("tank", "7a31ae09-61e0-414b-8124-68d1d2b89674")
	for _, want := range []string{"What=/dev/disk/by-uuid/7a31ae09", "Where=/srv/cwnsb/tank", "nofail", "Type=btrfs", "Managed by CrossWax NasBuster",
		// A missing disk must not hold multi-user.target; still unmounted at shutdown.
		"DefaultDependencies=no", "Conflicts=umount.target", "Before=umount.target"} {
		if !strings.Contains(unit, want) {
			t.Errorf("mount unit lacks %q", want)
		}
	}
	if strings.Contains(unit, "x-systemd.device-timeout") {
		t.Error("x-systemd.device-timeout is only read in /etc/fstab: it would only mislead")
	}
}

func labOutputs(t *testing.T) map[string]string {
	t.Helper()
	b, err := os.ReadFile("testdata/btrfs.txtar")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, s := range fixture.Sections(b) {
		out[s.Name] = string(s.Data)
	}
	return out
}

func TestParseBtrfsOutputs(t *testing.T) {
	o := labOutputs(t)
	devs, err := ParseDeviceStats([]byte(o["device-stats.json"]))
	if err != nil || len(devs) != 2 || devs[1].Errors.Read != 2 || devs[1].Errors.Total() != 3 || devs[0].Path != "/dev/loop2p1" {
		t.Fatalf("device stats = %+v, %v", devs, err)
	}
	df, err := ParseFilesystemDF([]byte(o["filesystem-df.json"]))
	if err != nil || len(df) != 4 || df[0].Type != "Data" || df[0].Profile != "RAID1" || df[2].Used != 131072 {
		t.Fatalf("df = %+v, %v", df, err)
	}
	size, used, free, missing := ParseFilesystemUsage(o["filesystem-usage.txt"])
	if size != 861888512 || used != 294912 || free != 379498496 || missing != 0 {
		t.Fatalf("usage = %d %d %d %d", size, used, free, missing)
	}
	// A fresh pool: a few KiB of alignment. After a bigger disk replaced a
	// member (the Pi 5, 2026-10-25): its extra space until a resize.
	if slack := ParseDeviceSlack(o["filesystem-usage.txt"]); slack != 7168 {
		t.Fatalf("slack = %d", slack)
	}
	if slack := ParseDeviceSlack("Overall:\n    Device missing:\t\t\t0\n    Device slack:\t\t1776294912\n"); slack != 1776294912 {
		t.Fatalf("slack after a replace = %d", slack)
	}
	s := ParseScrubStatus(o["scrub-status.txt"])
	if s.Status != "finished" || s.Duration != "0:00:00" || s.Started != "Thu Oct  1 21:59:52 2026" || s.Uncorrectable != 0 {
		t.Fatalf("scrub = %+v", s)
	}
	if want, _ := time.ParseInLocation(time.ANSIC, "Thu Oct  1 21:59:52 2026", time.Local); s.StartedAt != want.Unix() || s.DurationSeconds != 0 {
		t.Fatalf("scrub times = %d %d", s.StartedAt, s.DurationSeconds)
	}
	if parseHMS("0:41:07") != 2467 || parseHMS("26:00:01") != 93601 || parseHMS("x") != 0 {
		t.Fatal("parseHMS")
	}
	if never := ParseScrubStatus(o["scrub-status-never.txt"]); never.Status != "never" {
		t.Fatalf("never scrubbed = %+v", never)
	}
}

func TestParseMD(t *testing.T) {
	o := labOutputs(t)
	exp := ParseMDExport(o["mdadm-detail-export.txt"])
	if exp["MD_LEVEL"] != "raid1" || exp["MD_DEVNAME"] != "cwnsb-probe" || exp["MD_DEVICES"] != "2" {
		t.Fatalf("export = %v", exp)
	}
	arrays := ParseMDStat(o["mdstat.txt"])
	md127 := arrays["md127"]
	if md127.Level != "raid1" || md127.Active != 2 || md127.Expected != 2 || md127.Sync == nil || md127.Sync.Action != "resync" || md127.Sync.Percent != 14.1 {
		t.Fatalf("md127 = %+v sync=%+v", md127, md127.Sync)
	}
	if !slices.Equal(md127.Members, []string{"loop3p1", "loop2p1"}) || md127.Degraded() {
		t.Fatalf("md127 members = %v", md127.Members)
	}
	md126 := arrays["md126"]
	if md126.Level != "raid5" || !md126.Degraded() || md126.Sync != nil {
		t.Fatalf("md126 = %+v", md126)
	}
}

func TestComputeUsage(t *testing.T) {
	s := PoolStatus{FreeBytes: 1000, Profiles: []ProfileUsage{
		{Type: "Data", Used: 300}, {Type: "Metadata", Used: 20}, {Type: "System", Used: 1}, {Type: "GlobalReserve", Used: 500},
	}}
	s.ComputeUsage()
	if s.LogicalUsedBytes != 321 || s.UsableBytes != 1321 {
		t.Fatalf("usage = %d %d (GlobalReserve must not count)", s.LogicalUsedBytes, s.UsableBytes)
	}
}

func TestComputeHealth(t *testing.T) {
	s := PoolStatus{Mounted: true}
	s.ComputeHealth()
	if s.Health != HealthOK {
		t.Error(s.Health)
	}
	s.Devices = []PoolDevice{{Errors: DeviceErrors{Corruption: 1}}}
	s.ComputeHealth()
	if s.Health != HealthErrors {
		t.Error(s.Health)
	}
	s.MD = &MDStatus{Active: 2, Expected: 3}
	s.ComputeHealth()
	if s.Health != HealthDegraded {
		t.Error(s.Health)
	}
	s.Mounted = false
	s.ComputeHealth()
	if s.Health != HealthUnmounted {
		t.Error(s.Health)
	}
}
