// SPDX-License-Identifier: AGPL-3.0-or-later

package alerts

import (
	"slices"
	"strings"
	"testing"

	"github.com/crosswax/nasbuster/internal/storage"
	"github.com/crosswax/nasbuster/internal/system"
)

func boolp(b bool) *bool { return &b }

func ids(cs []Condition) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.ID + "/" + c.Level
	}
	return out
}

func TestEvaluate(t *testing.T) {
	pools := []PoolInput{
		{Name: "tank", Status: &storage.PoolStatus{Health: storage.HealthDegraded, MissingDevices: 1}},
		{Name: "data", Status: &storage.PoolStatus{Health: storage.HealthOK}},
		{Name: "unknown", Status: nil},
	}
	checks := []system.Check{
		{ID: "power", Code: "power.under_voltage_occurred"},
		{ID: "temperature", Code: "temperature.hot", Params: map[string]any{"celsius": 81.5}},
	}
	prev := storage.SmartReport{Available: true, Passed: boolp(true), Attributes: []storage.SmartAttribute{{ID: 199, Raw: 3}}}
	smart := []SmartInput{
		{DiskID: "wwn-a", Current: storage.SmartReport{Available: true, Passed: boolp(false)}},
		{DiskID: "wwn-b", Current: storage.SmartReport{Available: true, Passed: boolp(true),
			Attributes: []storage.SmartAttribute{{ID: 5, Raw: 8}, {ID: 199, Raw: 10}}}, Previous: &prev},
		{DiskID: "wwn-c", Current: storage.SmartReport{Available: false}},
	}
	got := ids(Evaluate(pools, checks, smart))
	want := []string{
		"pool.degraded:tank/crit",
		"power.under_voltage/warn",
		"smart.attributes:wwn-b/warn",
		"smart.crc:wwn-b/warn",
		"smart.failing:wwn-a/crit",
		"temperature.hot/crit",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("Evaluate =\n%v\nwant\n%v", got, want)
	}
}

// A fresh RAID5 builds its parity ([3/2], seen on the Pi 5): every disk is
// there, so not "a disk is missing" (crit) but "rebuilding" (warn).
func TestRebuildingIsNotAMissingDisk(t *testing.T) {
	recovery := &storage.MDSync{Action: "recovery", Percent: 1.5}
	building := &storage.MDStatus{Level: "raid5", Active: 2, Expected: 3, Members: []string{"sdd1", "sdc1", "sda1"}, Faulty: []string{}, Sync: recovery}
	missing := &storage.MDStatus{Level: "raid5", Active: 2, Expected: 3, Members: []string{"sdc1", "sda1"}, Faulty: []string{}}
	pools := []PoolInput{
		{Name: "e2", Status: &storage.PoolStatus{Health: storage.HealthDegraded, MD: building}},
		{Name: "e3", Status: &storage.PoolStatus{Health: storage.HealthDegraded, MD: missing, MissingDevices: 1}},
	}
	got := ids(Evaluate(pools, nil, nil))
	if want := []string{"pool.degraded:e3/crit", "pool.rebuilding:e2/warn"}; !slices.Equal(got, want) {
		t.Fatalf("Evaluate = %v, want %v", got, want)
	}
	// After the rebuild, only the scrub owed (seen on the Pi 5 at 12:03:21
	// and 00:18:21): every disk is there, the pool is being checked.
	complete := &storage.MDStatus{Level: "raid5", Active: 3, Expected: 3, Members: []string{"sdd1", "sdc1", "sda1"}, Faulty: []string{}}
	faulty := &storage.MDStatus{Level: "raid5", Active: 2, Expected: 3, Members: []string{"sdd1", "sdc1", "sda1"}, Faulty: []string{"sdd1"}}
	pools = []PoolInput{
		{Name: "owed", Status: &storage.PoolStatus{Health: storage.HealthDegraded, MD: complete, ResyncNeeded: true}},
		{Name: "mixed", Status: &storage.PoolStatus{Health: storage.HealthDegraded, MixedProfiles: true}},
		{Name: "failing", Status: &storage.PoolStatus{Health: storage.HealthDegraded, MD: faulty}},
	}
	got = ids(Evaluate(pools, nil, nil))
	if want := []string{"pool.degraded:failing/crit", "pool.rebuilding:mixed/warn", "pool.verifying:owed/warn"}; !slices.Equal(got, want) {
		t.Fatalf("Evaluate = %v, want %v", got, want)
	}
	// A hub cut took 2 of 3 members (the Pi 5, 2026-10-26): out of service,
	// not "degraded", and never "rebuilding".
	broken := &storage.MDStatus{Level: "raid5", Active: 1, Expected: 3, Members: []string{"sda1"}, Faulty: []string{}}
	got = ids(Evaluate([]PoolInput{{Name: "e2", Status: &storage.PoolStatus{Health: storage.HealthDegraded, MD: broken, MissingDevices: 2}}}, nil, nil))
	if want := []string{"pool.failed:e2/crit"}; !slices.Equal(got, want) {
		t.Fatalf("broken = %v, want %v", got, want)
	}
	// Every disk back (ADR-0032): one click restarts it, mounted or not. After
	// a reboot with 2 of 3 still gone: out of service, not "unmounted".
	back := &storage.Reassemble{Level: "raid5", Expected: 3, Members: []string{"sda1", "sdb1", "sdc1"}, Fresh: 1}
	short := &storage.Reassemble{Level: "raid5", Expected: 3, Members: []string{"sda1"}, Fresh: 1, Reason: storage.ReassembleMissingDisks}
	// Every disk there, but two claim one place: never forced, touch nothing.
	clash := &storage.Reassemble{Level: "raid5", Expected: 3, Members: []string{"sda1", "sdb1", "sdc1", "sdd1"}, Fresh: 3, Reason: storage.ReassembleRoles}
	pools = []PoolInput{
		{Name: "hot", Status: &storage.PoolStatus{Health: storage.HealthDegraded, MD: broken, MissingDevices: 2, CanReassemble: true, Reassemble: back}},
		{Name: "boot", Status: &storage.PoolStatus{Health: storage.HealthUnmounted, CanReassemble: true, Reassemble: back}},
		{Name: "short", Status: &storage.PoolStatus{Health: storage.HealthUnmounted, Reassemble: short}},
		{Name: "clash", Status: &storage.PoolStatus{Health: storage.HealthUnmounted, Reassemble: clash}},
	}
	conds := Evaluate(pools, nil, nil)
	if want := []string{"pool.failed:short/crit", "pool.manual_recovery:clash/crit", "pool.reassemble:boot/crit", "pool.reassemble:hot/crit"}; !slices.Equal(ids(conds), want) {
		t.Fatalf("reassemble = %v, want %v", ids(conds), want)
	}
	if got := Title("pool.failed", "en", conds[0].Params); !strings.Contains(got, "2 disks are gone") {
		t.Fatalf("title = %q", got)
	}
	if got := Title("pool.manual_recovery", "fr", conds[1].Params); !strings.Contains(got, "clash") || !strings.Contains(got, "récupération manuelle") {
		t.Fatalf("title = %q", got)
	}
	if got := Title("pool.rebuilding", "fr", map[string]any{"pool": "e2"}); !strings.Contains(got, "reconstruit sa redondance") {
		t.Fatalf("title = %q", got)
	}
}

func TestCRCOnlyWhenRising(t *testing.T) {
	same := storage.SmartReport{Available: true, Attributes: []storage.SmartAttribute{{ID: 199, Raw: 3}}}
	if got := Evaluate(nil, nil, []SmartInput{{DiskID: "d", Current: same, Previous: &same}}); len(got) != 0 {
		t.Fatalf("stable CRC count alerted: %v", ids(got))
	}
	if got := Evaluate(nil, nil, []SmartInput{{DiskID: "d", Current: same}}); len(got) != 0 {
		t.Fatalf("first read alerted on old CRC errors: %v", ids(got))
	}
}

func TestDiff(t *testing.T) {
	active := map[string]string{"pool.degraded:tank": Crit, "power.under_voltage": Warn, "temperature.hot": Crit}
	now := []Condition{
		{ID: "pool.degraded:tank", Level: Crit},  // still there: no notification
		{ID: "power.under_voltage", Level: Crit}, // got worse: notify again
		{ID: "smart.failing:wwn-a", Level: Crit}, // new
	}
	d := Diff(active, now)
	if got := ids(d.Raised); !slices.Equal(got, []string{"power.under_voltage/crit", "smart.failing:wwn-a/crit"}) {
		t.Fatalf("raised = %v", got)
	}
	if !slices.Equal(d.Resolved, []string{"temperature.hot"}) {
		t.Fatalf("resolved = %v", d.Resolved)
	}
}

func TestTitles(t *testing.T) {
	p := map[string]any{"pool": "tank"}
	if got := Title("pool.degraded", "fr", p); got != "Le pool tank est dégradé : un disque manque" {
		t.Fatalf("fr = %q", got)
	}
	if got := Title("pool.degraded", "de", p); got != "Pool tank is degraded: a disk is missing" {
		t.Fatalf("fallback = %q", got)
	}
	if got := ResolvedTitle("pool.degraded", "en", p); got != "Resolved: Pool tank is degraded: a disk is missing" {
		t.Fatalf("resolved = %q", got)
	}
	for lang, cat := range catalog {
		for code := range catalog["en"] {
			if _, ok := cat[code]; !ok {
				t.Errorf("%s lacks %s", lang, code)
			}
		}
	}
}

func TestDiskLabelHidesSerial(t *testing.T) {
	cases := map[string]string{
		"ata-Samsung_SSD_870_EVO_1TB_S6PUNX0R123456A": "Samsung SSD 870 EVO 1TB (…456A)",
		"usb-WD_Elements_25A3_575831314142-0:0":       "WD Elements 25A3 (…4142)",
		"nvme-Samsung_SSD_980_1TB_S64ANS0R123456":     "Samsung SSD 980 1TB (…3456)",
		"wwn-0x5000c500c1234567":                      "disk …4567",
		"nvme-eui.0025385b71b07e2f":                   "disk …7e2f",
		"weird":                                       "disk …eird",
	}
	for id, want := range cases {
		if got := DiskLabel(id); got != want {
			t.Errorf("DiskLabel(%q) = %q, want %q", id, got, want)
		}
	}
	p := Public(map[string]any{"disk": "ata-Samsung_SSD_870_EVO_1TB_S6PUNX0R123456A", "count": 3})
	if p["disk"] != "Samsung SSD 870 EVO 1TB (…456A)" || p["count"] != 3 {
		t.Fatalf("Public = %v", p)
	}
}

func TestMissingDiskSaysWhyThePoolIsDown(t *testing.T) {
	pools := []PoolInput{
		{Name: "tank", Status: &storage.PoolStatus{Health: storage.HealthUnmounted, CanMountDegraded: true, MissingDevices: 1}},
		{Name: "data", Status: &storage.PoolStatus{Health: storage.HealthUnmounted}},
	}
	got := ids(Evaluate(pools, nil, nil))
	if !slices.Equal(got, []string{"pool.missing_disk:tank/crit", "pool.unmounted:data/crit"}) {
		t.Fatalf("got %v", got)
	}
	if Title("pool.missing_disk", "fr", map[string]any{"pool": "tank"}) != "Le pool tank ne peut pas démarrer : un disque manque" {
		t.Fatal(Title("pool.missing_disk", "fr", map[string]any{"pool": "tank"}))
	}
}
