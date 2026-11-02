// SPDX-License-Identifier: AGPL-3.0-or-later

package scheduler

import (
	"context"
	"testing"
	"time"
)

// The SD card fills up (ADR-0034): the alert is raised once, holds while the
// card stays near the threshold, and is resolved once well below it.
func TestSystemDiskSpaceAlert(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	const gb = 1_000_000_000
	used := uint64(88 * gb)
	f.sched.RootSpace = func() (uint64, uint64, bool) { return used, 100 * gb, true }

	step := func(percent uint64) (raised, resolved int, level string) {
		used = percent * gb
		f.clock = f.clock.Add(6 * time.Minute)
		r := f.sched.Tick(ctx)
		active, err := f.st.ActiveAlerts(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return r.AlertsRaised, r.AlertsResolved, active["system.space"]
	}
	if raised, _, level := step(88); raised != 1 || level != "warn" {
		t.Fatalf("88 %%: raised %d, level %q", raised, level)
	}
	if raised, _, level := step(89); raised != 0 || level != "warn" {
		t.Fatalf("89 %%: raised again (%d) or level %q", raised, level)
	}
	if raised, _, level := step(96); raised != 1 || level != "crit" {
		t.Fatalf("96 %%: raised %d, level %q (worse: notified again)", raised, level)
	}
	if _, resolved, level := step(84); resolved != 0 || level == "" {
		t.Fatalf("84 %%: resolved %d, level %q (within the margin: holds)", resolved, level)
	}
	if _, resolved, level := step(80); resolved != 1 || level != "" {
		t.Fatalf("80 %%: resolved %d, level %q", resolved, level)
	}
}
