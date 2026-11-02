// SPDX-License-Identifier: AGPL-3.0-or-later

package resync

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/crosswax/nasbuster/internal/storage"
	"github.com/crosswax/nasbuster/internal/store"
)

type memSettings map[string][]byte

func (m memSettings) GetSetting(_ context.Context, key string, v any) error {
	b, ok := m[key]
	if !ok {
		return store.ErrNotFound
	}
	return json.Unmarshal(b, v)
}

func (m memSettings) SetSetting(_ context.Context, key string, v any) error {
	b, err := json.Marshal(v)
	m[key] = b
	return err
}

func healthy(scrubStarted int64) storage.PoolStatus {
	st := storage.PoolStatus{Name: "e1", Mounted: true, Scrub: storage.ScrubStatus{Status: "finished", StartedAt: scrubStarted}}
	st.ComputeHealth()
	return st
}

func TestDegradedPeriodOwesAScrub(t *testing.T) {
	ctx := context.Background()
	s := memSettings{}
	t0 := time.Unix(1_792_833_000, 0)

	// A disk is pulled out while mounted: the pool gets marked.
	st := storage.PoolStatus{Name: "e1", Mounted: true, MissingDevices: 1}
	if err := Apply(ctx, s, &st, t0, "b1"); err != nil || !st.ResyncNeeded {
		t.Fatalf("degraded: %+v %v", st, err)
	}

	// Every disk is back and the last scrub predates the mark: still owed, and
	// the pool is not "ok" (on the Pi, 101,015 blocks had a single copy here).
	st = healthy(t0.Unix() - 600)
	if err := Apply(ctx, s, &st, t0.Add(time.Hour), "b1"); err != nil || !st.ResyncNeeded || st.Health != storage.HealthDegraded {
		t.Fatalf("back, old scrub: %+v %v", st, err)
	}

	// A scrub started afterwards finished: the mark goes, the pool is ok.
	st = healthy(t0.Unix() + 3600)
	if err := Apply(ctx, s, &st, t0.Add(2*time.Hour), "b1"); err != nil || st.ResyncNeeded || st.Health != storage.HealthOK {
		t.Fatalf("after the scrub: %+v %v", st, err)
	}
	st = healthy(t0.Unix() + 3600)
	Apply(ctx, s, &st, t0.Add(3*time.Hour), "b1")
	if st.ResyncNeeded {
		t.Fatal("the mark came back")
	}
}

func TestReplaceClearsTheMark(t *testing.T) {
	ctx := context.Background()
	s := memSettings{}
	t0 := time.Unix(1_792_833_000, 0)
	Mark(ctx, s, "e1", t0) // mounted degraded

	// During the replace the disk is still missing: nothing changes.
	st := storage.PoolStatus{Name: "e1", Mounted: true, MountedDegraded: true, MissingDevices: 1,
		Replace: &storage.ReplaceStatus{State: "running", Percent: 40}}
	Apply(ctx, s, &st, t0.Add(time.Minute), "b1")
	if !st.ResyncNeeded {
		t.Fatal("mark lost during the replace")
	}
	// Finished: the new disk was rebuilt from the good copies. Still mounted
	// "degraded" until the next boot, but nothing is owed any more.
	st = storage.PoolStatus{Name: "e1", Mounted: true, MountedDegraded: true,
		Replace: &storage.ReplaceStatus{State: "finished", FinishedAt: t0.Unix() + 600}}
	Apply(ctx, s, &st, t0.Add(time.Hour), "b1")
	if st.ResyncNeeded || st.Health != storage.HealthOK {
		t.Fatalf("after the replace: %+v", st)
	}
	// An old replace does not clear a newer mark.
	Mark(ctx, s, "e1", t0.Add(2*time.Hour))
	Apply(ctx, s, &st, t0.Add(3*time.Hour), "b1")
	if !st.ResyncNeeded {
		t.Fatal("a replace from before the mark cleared it")
	}
}

func TestMarkKeepsTheFirstDate(t *testing.T) {
	ctx := context.Background()
	s := memSettings{}
	t0 := time.Unix(1_792_833_000, 0)
	if err := Mark(ctx, s, "e1", t0); err != nil {
		t.Fatal(err)
	}
	Mark(ctx, s, "e1", t0.Add(time.Hour)) // a later degraded mount
	// A scrub started between the two still counts: it ran after the first mark.
	st := healthy(t0.Unix() + 60)
	Apply(ctx, s, &st, t0.Add(2*time.Hour), "b1")
	if st.ResyncNeeded {
		t.Fatalf("mark = %s", s[settingKey])
	}
}

func TestUnmountedPoolIsNotMarked(t *testing.T) {
	// Booted without a disk, not mounted: nothing was written yet.
	s := memSettings{}
	st := storage.PoolStatus{Name: "e1", MissingDevices: 1, CanMountDegraded: true}
	Apply(context.Background(), s, &st, time.Now(), "b1")
	if st.ResyncNeeded || len(s) != 0 {
		t.Fatalf("unmounted pool marked: %+v %s", st, s[settingKey])
	}
	Forget(context.Background(), s, "e1")
}

func TestResumedScrubDoesNotClearTheMark(t *testing.T) {
	ctx := context.Background()
	s := memSettings{}
	t0 := time.Unix(1_792_833_000, 0)
	Mark(ctx, s, "e1", t0)
	// Resumed after the mark, but its first pass may predate it.
	st := healthy(t0.Unix() + 600)
	st.Scrub.Resumed = true
	Apply(ctx, s, &st, t0.Add(time.Hour), "b1")
	if !st.ResyncNeeded {
		t.Fatal("a resumed scrub cleared the mark")
	}
}

// parity is a mounted RAID5 pool whose array runs complete; md is what md says.
func parity(md storage.MDStatus, scrubStarted int64) storage.PoolStatus {
	md.Level, md.Expected = "raid5", 3
	if md.Active == 0 {
		md.Active = 3
	}
	st := healthy(scrubStarted)
	st.Name, st.MD = "e2", &md
	return st
}

func TestBrokenParityOwesRepairAndScrub(t *testing.T) {
	ctx := context.Background()
	s := memSettings{}
	t0 := time.Unix(1_792_833_000, 0)

	// A hub cut, seen before anyone acts: whatever brings the pool back (the
	// button, a manual recovery), the repair and the scrub are owed.
	st := parity(storage.MDStatus{State: "broken", Active: 1}, t0.Unix()-600)
	if err := Apply(ctx, s, &st, t0, "b1"); err != nil || !st.RepairNeeded || !st.ResyncNeeded {
		t.Fatalf("broken: %+v %v", st, err)
	}
	// After a reboot, every disk back, the array held: still owed.
	held := storage.PoolStatus{Name: "e2", CanReassemble: true}
	if Apply(ctx, s, &held, t0.Add(time.Hour), "b2"); !held.RepairNeeded || !held.ResyncNeeded {
		t.Fatalf("held: %+v", held)
	}
	// Reassembled, a scrub already done after the failure: the repair still
	// comes first, and the scrub mark waits for it.
	st = parity(storage.MDStatus{}, t0.Unix()+600)
	if Apply(ctx, s, &st, t0.Add(2*time.Hour), "b2"); !st.RepairNeeded || !st.ResyncNeeded || st.Health != storage.HealthDegraded {
		t.Fatalf("back, no repair: %+v", st)
	}
	// One disk missing at boot is no failure: a degraded mount, no repair.
	s2 := memSettings{}
	missing := storage.PoolStatus{Name: "e2", CanMountDegraded: true,
		Reassemble: &storage.Reassemble{Level: "raid5", Expected: 3, Members: []string{"sda1", "sdc1"}, Fresh: 2, Reason: storage.ReassembleMissingDisks}}
	if Apply(ctx, s2, &missing, t0, "b1"); missing.RepairNeeded {
		t.Fatalf("one disk missing at boot owes a repair: %+v", missing)
	}
}

func TestRepairMarkGoesOnlyAtItsEnd(t *testing.T) {
	ctx := context.Background()
	t0 := time.Unix(1_792_833_000, 0)
	broken := parity(storage.MDStatus{State: "broken", Active: 1}, 0)
	idle := func(interrupted bool) storage.PoolStatus {
		return parity(storage.MDStatus{Interrupted: interrupted}, t0.Unix()+7200)
	}
	repairing := parity(storage.MDStatus{Sync: &storage.MDSync{Action: "repair", Percent: 40}}, 0)

	for _, tc := range []struct {
		name  string
		steps []storage.PoolStatus // seen after the repair started in boot b1
		boot  string               // boot of the last step
		owed  bool
	}{
		{"to its end", []storage.PoolStatus{repairing, idle(false)}, "b1", false},
		{"ended before the next pass", []storage.PoolStatus{idle(false)}, "b1", false},
		// md does not resume a repair after a reboot: idle, sync_min at 0.
		{"cut by a reboot", []storage.PoolStatus{repairing, idle(false)}, "b2", true},
		// Stopped halfway (echo idle): md keeps where in sync_min.
		{"stopped halfway", []storage.PoolStatus{repairing, idle(true)}, "b1", true},
		// A member dropped: md stopped the repair; back from the bitmap, the
		// rebuild ends idle in the same boot.
		{"a member dropped", []storage.PoolStatus{repairing, parity(storage.MDStatus{Active: 2}, 0),
			parity(storage.MDStatus{Sync: &storage.MDSync{Action: "recovery", Percent: 50}}, 0), idle(false)}, "b1", true},
		// The array broke again during the repair: owed, from the new failure.
		{"broken again", []storage.PoolStatus{repairing, broken, idle(false)}, "b1", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := memSettings{}
			st := broken
			Apply(ctx, s, &st, t0, "b1")
			if err := RepairStarted(ctx, s, "e2", t0.Add(time.Hour), "b1"); err != nil {
				t.Fatal(err)
			}
			for i, step := range tc.steps {
				boot := "b1"
				if i == len(tc.steps)-1 {
					boot = tc.boot
				}
				st = step
				if err := Apply(ctx, s, &st, t0.Add(time.Duration(2+i)*time.Hour), boot); err != nil {
					t.Fatal(err)
				}
			}
			if st.RepairNeeded != tc.owed {
				t.Fatalf("repair owed = %v, want %v (%s)", st.RepairNeeded, tc.owed, s[repairKey])
			}
			// Whatever the end, the scrub mark waits for the repair, and goes
			// once both are done.
			if st.RepairNeeded && !st.ResyncNeeded {
				t.Fatal("scrub mark cleared before the repair")
			}
			if !tc.owed && st.ResyncNeeded {
				t.Fatalf("scrub after the repair, mark kept: %+v", st)
			}
		})
	}
}

func TestForgetDropsTheRepairRun(t *testing.T) {
	ctx := context.Background()
	s := memSettings{}
	st := parity(storage.MDStatus{State: "broken", Active: 1}, 0)
	Apply(ctx, s, &st, time.Unix(1_792_833_000, 0), "b1")
	RepairStarted(ctx, s, "e2", time.Unix(1_792_836_600, 0), "b1")
	if err := Forget(ctx, s, "e2"); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{settingKey, repairKey, runKey} {
		if string(s[key]) != "{}" {
			t.Fatalf("%s = %s after Forget", key, s[key])
		}
	}
}
