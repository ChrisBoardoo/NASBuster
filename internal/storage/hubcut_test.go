// SPDX-License-Identifier: AGPL-3.0-or-later

package storage

import (
	"os"
	"slices"
	"testing"

	"github.com/crosswax/nasbuster/internal/fixture"
)

func hubcutOutputs(t *testing.T) map[string]string {
	t.Helper()
	b, err := os.ReadFile("testdata/hubcut.txtar")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, s := range fixture.Sections(b) {
		out[s.Name] = string(s.Data)
	}
	return out
}

// The states of an array that lost 2 of its 3 members, as the lab and the Pi 5
// print them (ADR-0032).
func TestParseMDStatAfterAHubCut(t *testing.T) {
	o := hubcutOutputs(t)
	for name, want := range map[string]MDStatus{
		// Kernel 5.15 still says "active"; 6.18 says "broken": both are broken.
		"mdstat-broken-lab.txt": {State: "active", Level: "raid5", Active: 1, Expected: 3, Members: []string{"loop2"}},
		"mdstat-broken-pi5.txt": {State: "broken", Level: "raid5", Active: 1, Expected: 3, Members: []string{"sda1"}},
		// After a reboot: members held as spares, array not started, no level.
		"mdstat-inactive-pi5.txt": {State: "inactive", Members: []string{"sdd1", "sdc1", "sda1"}},
		// Right after a forced assembly.
		"mdstat-auto-read-only-pi5.txt": {State: "active", Level: "raid5", Active: 3, Expected: 3, Members: []string{"sda1", "sdd1", "sdc1"}},
	} {
		got, ok := ParseMDStat(o[name])["md127"]
		if !ok || got.State != want.State || got.Level != want.Level || got.Active != want.Active ||
			got.Expected != want.Expected || !slices.Equal(got.Members, want.Members) {
			t.Errorf("%s = %+v, want %+v", name, got, want)
		}
	}
	if broken := ParseMDStat(o["mdstat-broken-pi5.txt"])["md127"]; !broken.Broken() {
		t.Error("2 of 3 RAID5 members gone must be broken")
	}
	if fine := ParseMDStat(o["mdstat-auto-read-only-pi5.txt"])["md127"]; fine.Broken() || fine.Degraded() {
		t.Errorf("auto-read-only after a forced assembly = %+v", fine)
	}
}

func TestParseMDExamine(t *testing.T) {
	o := hubcutOutputs(t)
	e, ok := ParseMDExamine(o["examine-pi5.txt"])
	if !ok || e.ArrayUUID != "30ab94bd:026ffe21:8fd777ae:9d3632d2" || e.RaidDevices != 3 || e.Events != 762 || e.Role != 0 {
		t.Fatalf("examine = %+v %v", e, ok)
	}
	spare, ok := ParseMDExamine("     Array UUID : 30ab94bd:026ffe21:8fd777ae:9d3632d2\n   Raid Devices : 3\n         Events : 760\n   Device Role : spare\n")
	if !ok || spare.Role != -1 {
		t.Fatalf("spare = %+v %v", spare, ok)
	}
	if _, ok := ParseMDExamine("mdadm: No md superblock detected on /dev/sdb1.\n"); ok {
		t.Fatal("no superblock parsed as a member")
	}
}
