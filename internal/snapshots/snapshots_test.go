// SPDX-License-Identifier: AGPL-3.0-or-later

package snapshots

import (
	"slices"
	"testing"
	"time"
)

func TestNames(t *testing.T) {
	ts := time.Date(2026, 10, 1, 21, 0, 5, 0, time.FixedZone("CEST", 2*3600))
	n := Name(ts)
	if n != "@GMT-2026.10.01-19.00.05" {
		t.Fatalf("Name = %s (must be UTC)", n)
	}
	back, ok := Parse(n)
	if !ok || !back.Equal(ts) {
		t.Fatalf("Parse = %v %v", back, ok)
	}
	for _, bad := range []string{"", "@GMT-2026.10.01", "../@GMT-2026.10.01-19.00.05", "@GMT-2026.13.01-19.00.05", "x@GMT-2026.10.01-19.00.05", "@GMT-2026.10.01-19.00.05/x"} {
		if ValidName(bad) {
			t.Errorf("ValidName(%q) = true", bad)
		}
	}
}

// simulate takes hourly snapshots for `hours` hours, pruning after each one
// like the scheduler does, and returns what is left.
func simulate(start time.Time, hours int, p Policy) []string {
	var have []string
	for h := 0; h < hours; h++ {
		have = append(have, Name(start.Add(time.Duration(h)*time.Hour)))
		del := Prune(have, p)
		have = slices.DeleteFunc(have, func(n string) bool { return slices.Contains(del, n) })
	}
	return have
}

func TestPruneSteadyState(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	have := simulate(start, 24*400, DefaultPolicy) // more than a year of hourly snapshots
	// Upper bound: every bucket kept separately (overlaps make it smaller).
	if max := 24 + 7 + 4 + 6; len(have) > max || len(have) < 24 {
		t.Fatalf("kept %d snapshots, want between 24 and %d", len(have), max)
	}
	newest := Name(start.Add(time.Duration(24*400-1) * time.Hour))
	if !slices.Contains(have, newest) {
		t.Fatal("the newest snapshot must always be kept")
	}
	// Monthly buckets are calendar months (this one + 5 before): the oldest
	// kept snapshot is 4 to 6 months old depending on the day of the month.
	oldest, _ := Parse(slices.Min(have))
	newestT, _ := Parse(newest)
	if age := newestT.Sub(oldest); age < 120*24*time.Hour || age > 186*24*time.Hour {
		t.Fatalf("oldest kept is %v old, want 4 to 6 months", age)
	}
}

func TestPruneKeepsRecentHours(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	have := simulate(start, 30, DefaultPolicy)
	for h := 6; h < 30; h++ { // the last 24 hours are all there
		if !slices.Contains(have, Name(start.Add(time.Duration(h)*time.Hour))) {
			t.Errorf("hour %d missing", h)
		}
	}
}

func TestPruneIgnoresForeignNames(t *testing.T) {
	names := []string{"my-backup", "@GMT-2026.10.01-19.00.05", "@GMT-2020.01.01-00.00.00", "README"}
	del := Prune(names, Policy{Hourly: 1})
	if !slices.Equal(del, []string{"@GMT-2020.01.01-00.00.00"}) {
		t.Fatalf("delete = %v (foreign names must never be deleted)", del)
	}
}

func TestPolicyValid(t *testing.T) {
	if !DefaultPolicy.Valid() || (Policy{}).Valid() || (Policy{Hourly: -1, Daily: 1}).Valid() || (Policy{Hourly: 100000}).Valid() {
		t.Fatal("policy bounds")
	}
}
