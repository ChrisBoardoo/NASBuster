// SPDX-License-Identifier: AGPL-3.0-or-later

package storage

import (
	"os"
	"slices"
	"testing"
	"time"

	"github.com/crosswax/nasbuster/internal/fixture"
)

func degradedOutputs(t *testing.T) map[string]string {
	t.Helper()
	b, err := os.ReadFile("testdata/degraded.txtar")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, s := range fixture.Sections(b) {
		out[s.Name] = string(s.Data)
	}
	return out
}

func TestParseFilesystemShowMissing(t *testing.T) {
	o := degradedOutputs(t)

	unmounted := ParseFilesystemShow(o["show-unmounted-missing.txt"])
	if unmounted.Total != 2 || !unmounted.SomeMissing || len(unmounted.Devices) != 1 || unmounted.MissingCount() != 1 {
		t.Fatalf("unmounted = %+v", unmounted)
	}
	if len(unmounted.MissingDevIDs()) != 0 { // btrfs does not name it while unmounted
		t.Fatalf("unmounted missing ids = %v", unmounted.MissingDevIDs())
	}

	mounted := ParseFilesystemShow(o["show-mounted-missing.txt"])
	if mounted.MissingCount() != 1 || !slices.Equal(mounted.MissingDevIDs(), []int{2}) {
		t.Fatalf("mounted degraded = %+v", mounted)
	}
	if id, ok := mounted.DevIDOf("loop2p1"); !ok || id != 1 || mounted.Devices[0].SizeBytes != 430944256 {
		t.Fatalf("present device = %+v", mounted.Devices[0])
	}
	if _, ok := mounted.DevIDOf("MISSING"); ok {
		t.Fatal("a missing device must not match a kernel name")
	}

	after := ParseFilesystemShow(o["show-after-replace.txt"])
	if after.MissingCount() != 0 || len(after.Devices) != 2 {
		t.Fatalf("after replace = %+v", after)
	}
	if id, _ := after.DevIDOf("loop4p1"); id != 2 {
		t.Fatalf("the new disk takes the replaced devid, got %d", id)
	}
}

func TestParseDeviceStatsMissingAndFailing(t *testing.T) {
	o := degradedOutputs(t)
	devs, err := ParseDeviceStats([]byte(o["stats-missing.json"]))
	if err != nil || len(devs) != 2 || devs[1].Path != "devid:2" {
		t.Fatalf("missing = %+v %v", devs, err)
	}
	devs, err = ParseDeviceStats([]byte(o["stats-failing.json"]))
	if err != nil || devs[1].Errors.Write != 326 || devs[0].Errors.Total() != 0 {
		t.Fatalf("failing = %+v %v", devs, err)
	}
}

func TestParseReplaceAndBalance(t *testing.T) {
	o := degradedOutputs(t)
	if r := ParseReplaceStatus(o["replace-never.txt"]); r != nil {
		t.Fatalf("never = %+v", r)
	}
	if r := ParseReplaceStatus(o["replace-running.txt"]); r == nil || r.State != "running" || r.Percent != 0 {
		t.Fatalf("running = %+v", r)
	}
	if r := ParseReplaceStatus("45.7% done, 3 write errs, 1 uncorr. read errs"); r == nil || r.Percent != 45.7 || r.WriteErrors != 3 || r.ReadErrors != 1 {
		t.Fatalf("running with errors = %+v", r)
	}
	defer func() { replaceNow = time.Now }()
	replaceNow = func() time.Time { return time.Date(2026, 10, 24, 12, 0, 0, 0, time.Local) }
	finished := time.Date(2026, 10, 2, 0, 2, 13, 0, time.Local).Unix()
	if r := ParseReplaceStatus(o["replace-finished.txt"]); r == nil || r.State != "finished" || r.Percent != 100 || r.FinishedAt != finished {
		t.Fatalf("finished = %+v", r)
	}
	// btrfs prints no year: a date "in the future" is last year's.
	replaceNow = func() time.Time { return time.Date(2027, 1, 3, 9, 0, 0, 0, time.Local) }
	if r := ParseReplaceStatus("Started on 30.Dec 22:00:00, finished on 30.Dec 23:00:00, 0 write errs, 0 uncorr. read errs"); r == nil ||
		r.FinishedAt != time.Date(2026, 12, 30, 23, 0, 0, 0, time.Local).Unix() {
		t.Fatalf("across new year = %+v", r)
	}
	if b := ParseBalanceStatus(o["balance-none.txt"]); b != nil {
		t.Fatalf("none = %+v", b)
	}
	if b := ParseBalanceStatus(o["balance-running.txt"]); b == nil || b.State != "running" || b.Total != 10 || b.PercentLeft != 100 {
		t.Fatalf("running = %+v", b)
	}
	if b := ParseBalanceStatus(o["balance-paused.txt"]); b == nil || b.State != "paused" || b.Done != 1 || b.PercentLeft != 90 {
		t.Fatalf("paused = %+v", b)
	}
}

func TestMixedProfilesAfterDegradedWrites(t *testing.T) {
	o := degradedOutputs(t)
	df, err := ParseFilesystemDF([]byte(o["df-mixed.json"]))
	if err != nil {
		t.Fatal(err)
	}
	if !MixedProfiles(df) {
		t.Fatal("single chunks next to RAID1 ones must be reported")
	}
	if d, m := TargetProfiles(df); d != "raid1" || m != "raid1" {
		t.Fatalf("targets = %q %q", d, m)
	}
	clean := []ProfileUsage{{Type: "Data", Profile: "RAID1"}, {Type: "Metadata", Profile: "RAID1C3"}, {Type: "System", Profile: "RAID1C3"},
		{Type: "GlobalReserve", Profile: "single"}}
	if MixedProfiles(clean) {
		t.Fatal("GlobalReserve is always single and must not count")
	}
	if d, m := TargetProfiles(clean); d != "raid1" || m != "raid1c3" {
		t.Fatalf("targets = %q %q", d, m)
	}
	single := []ProfileUsage{{Type: "Data", Profile: "single"}, {Type: "Metadata", Profile: "DUP"}}
	if d, m := TargetProfiles(single); d != "" || m != "dup" {
		t.Fatalf("single layout targets = %q %q", d, m)
	}
}

func TestMDFaultyAndRecovery(t *testing.T) {
	o := degradedOutputs(t)
	faulty := ParseMDStat(o["mdstat-faulty.txt"])["md127"]
	if !slices.Equal(faulty.Faulty, []string{"loop7p1"}) || !faulty.Degraded() || faulty.Active != 2 || faulty.Expected != 3 {
		t.Fatalf("faulty = %+v", faulty)
	}
	// mdadm --detail --export does not flag the faulty member: /proc/mdstat is the truth.
	if exp := ParseMDExport(o["md-export-faulty.txt"]); exp["MD_DEVICE_dev_loop7p1_ROLE"] != "1" {
		t.Fatalf("export = %v", exp)
	}
	rec := ParseMDStat(o["mdstat-recovery.txt"])["md127"]
	if len(rec.Faulty) != 0 || !rec.Degraded() || rec.Sync == nil || rec.Sync.Action != "recovery" || rec.Sync.Percent != 0.4 {
		t.Fatalf("recovery = %+v sync=%+v", rec, rec.Sync)
	}
	// The member being rebuilt is listed: every disk is there.
	if !rec.Rebuilding() || faulty.Rebuilding() {
		t.Fatalf("rebuilding: recovery %v, faulty %v", rec.Rebuilding(), faulty.Rebuilding())
	}
	// More members gone than the level tolerates: broken (no data at all).
	for _, c := range []struct {
		level          string
		active, expect int
		broken         bool
	}{
		{"raid5", 2, 3, false}, {"raid5", 1, 3, true}, {"raid6", 2, 4, false}, {"raid6", 1, 4, true},
		{"raid1", 1, 3, false}, {"raid1", 0, 2, true}, {"", 2, 3, true}, // unknown level: fail closed
	} {
		if got := (MDStatus{Level: c.level, Active: c.active, Expected: c.expect}).Broken(); got != c.broken {
			t.Errorf("%s %d/%d broken = %v", c.level, c.active, c.expect, got)
		}
	}
}

func TestHealthWithLostRedundancy(t *testing.T) {
	s := PoolStatus{Mounted: true, MixedProfiles: true}
	s.ComputeHealth()
	if s.Health != HealthDegraded {
		t.Fatalf("mixed profiles = %s, want degraded", s.Health)
	}
	s = PoolStatus{Mounted: true, MissingDevices: 1}
	s.ComputeHealth()
	if s.Health != HealthDegraded {
		t.Fatalf("missing device = %s", s.Health)
	}
}

func TestParseScrubResumed(t *testing.T) {
	s := ParseScrubStatus("UUID: x\nScrub resumed:    Sat Oct 24 18:10:00 2026\nStatus:           finished\nDuration:         0:01:00\n")
	if !s.Resumed || s.Status != "finished" || s.StartedAt == 0 {
		t.Fatalf("resumed = %+v", s)
	}
	if s = ParseScrubStatus("Scrub started:    Sat Oct 24 18:10:00 2026\nStatus:           aborted\n"); s.Resumed || s.Status != "aborted" {
		t.Fatalf("aborted = %+v", s)
	}
}
