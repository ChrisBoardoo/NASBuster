// SPDX-License-Identifier: AGPL-3.0-or-later

// Package resync remembers, per pool, since when a scrub is owed (ADR-0027).
// After a degraded period, "every disk is back" does not mean that both copies
// are good again: blocks written meanwhile have one copy, and a disk that came
// back holds stale data. Only a scrub started afterwards checks and repairs
// them. Measured on the Pi 5: the pool said "ok" while 101,015 blocks still
// lacked their second copy.
package resync

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/crosswax/nasbuster/internal/storage"
	"github.com/crosswax/nasbuster/internal/store"
)

// Settings is the part of the store this package needs.
type Settings interface {
	GetSetting(ctx context.Context, key string, v any) error
	SetSetting(ctx context.Context, key string, v any) error
}

// settingKey holds {pool: unix seconds since a scrub is owed}.
const settingKey = "pools.resync"

// repairKey holds {pool: unix seconds since a parity repair is owed}: a parity
// pool seen beyond its tolerance (ADR-0032) may have stripes written at the
// failure with a wrong parity. md repairs it once every member is in sync,
// then the scrub runs.
const repairKey = "pools.repair"

// runKey holds {pool: run}: the parity repair md is doing. The repair mark
// goes only once the repair is seen at its end, in the boot it started in.
const runKey = "pools.repair_run"

// run is a parity repair the scheduler started.
type run struct {
	At   int64  `json:"at"`   // unix seconds
	Boot string `json:"boot"` // boot id: md does not resume a repair after a reboot
}

// mu serializes the read-modify-write of the settings (API and scheduler).
var mu sync.Mutex

func load(ctx context.Context, s Settings, key string) (map[string]int64, error) {
	marks := map[string]int64{}
	if err := s.GetSetting(ctx, key, &marks); err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	return marks, nil
}

func loadRuns(ctx context.Context, s Settings) (map[string]run, error) {
	runs := map[string]run{}
	if err := s.GetSetting(ctx, runKey, &runs); err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	return runs, nil
}

// Mark records that pool owes a scrub since at, unless it already does. Pass
// the time taken BEFORE the operation: a scrub it starts must count.
func Mark(ctx context.Context, s Settings, pool string, at time.Time) error {
	return mark(ctx, s, settingKey, pool, at)
}

func mark(ctx context.Context, s Settings, key, pool string, at time.Time) error {
	mu.Lock()
	defer mu.Unlock()
	marks, err := load(ctx, s, key)
	if err != nil {
		return err
	}
	if marks[pool] != 0 {
		return nil
	}
	marks[pool] = at.Unix()
	return s.SetSetting(ctx, key, marks)
}

// RepairStarted records that md started the parity repair pool owes, in this
// boot. The mark stays: Apply drops it once it sees the repair at its end.
func RepairStarted(ctx context.Context, s Settings, pool string, at time.Time, boot string) error {
	mu.Lock()
	defer mu.Unlock()
	runs, err := loadRuns(ctx, s)
	if err != nil {
		return err
	}
	runs[pool] = run{At: at.Unix(), Boot: boot}
	return s.SetSetting(ctx, runKey, runs)
}

// Apply brings the marks up to date with a fresh status, then sets
// st.RepairNeeded, st.ResyncNeeded and the health. boot is the current boot id.
//
// A parity pool seen beyond its tolerance owes a parity repair and a scrub,
// whatever brings it back. The repair mark goes once the repair is seen at its
// end (repairEnded). A pool seen mounted with a disk missing owes a scrub (not
// during a replace: it rebuilds that disk); that mark goes once every disk is
// in use, no repair is owed, and a scrub or a replace finished after it.
func Apply(ctx context.Context, s Settings, st *storage.PoolStatus, now time.Time, boot string) error {
	mu.Lock()
	defer mu.Unlock()
	marks, err := load(ctx, s, settingKey)
	if err != nil {
		return err
	}
	repairs, err := load(ctx, s, repairKey)
	if err != nil {
		return err
	}
	runs, err := loadRuns(ctx, s)
	if err != nil {
		return err
	}
	failed := st.ParityFailed()
	repairChanged, runChanged := false, false
	r, running := runs[st.Name]
	switch {
	case failed:
		if repairs[st.Name] == 0 {
			repairs[st.Name], repairChanged = now.Unix(), true
		}
		if running { // stopped with the array
			delete(runs, st.Name)
			runChanged = true
		}
	case running && repairs[st.Name] == 0:
		delete(runs, st.Name)
		runChanged = true
	case running:
		if done, over := repairEnded(st.MD, r, boot); over {
			delete(runs, st.Name)
			runChanged = true
			if done {
				delete(repairs, st.Name)
				repairChanged = true
			}
		}
	}
	st.RepairNeeded = repairs[st.Name] != 0

	since, changed := marks[st.Name], false
	missing := st.MissingDevices > 0
	replacing := st.Replace != nil && st.Replace.State == "running"
	switch {
	case (failed || (st.Mounted && missing && !replacing)) && since == 0:
		since, changed = now.Unix(), true
		marks[st.Name] = since
	case since != 0 && !missing && !st.RepairNeeded && checked(st, since):
		since, changed = 0, true
		delete(marks, st.Name)
	}
	st.ResyncNeeded = since != 0
	st.ComputeHealth()
	// The repair first: it is what a failure must not lose.
	if repairChanged {
		if err := s.SetSetting(ctx, repairKey, repairs); err != nil {
			return err
		}
	}
	if runChanged {
		if err := s.SetSetting(ctx, runKey, runs); err != nil {
			return err
		}
	}
	if changed {
		return s.SetSetting(ctx, settingKey, marks)
	}
	return nil
}

// repairEnded reads a repair run against the array: over once md no longer
// does it; done only if it went to its end in the boot it started in. md does
// not resume a repair after a reboot, stops it when a member drops, and keeps
// in md/sync_min where one stopped halfway (Interrupted).
func repairEnded(md *storage.MDStatus, r run, boot string) (done, over bool) {
	switch {
	case md == nil || md.Degraded():
		return false, true // the array stopped, or lost a member
	case md.Sync != nil:
		return false, md.Sync.Action != "repair" // a rebuild or a check: the repair stopped
	case r.Boot != boot:
		return false, true
	}
	return !md.Interrupted, true
}

// checked: a scrub, or a replace, finished after since. A replace rebuilds the
// new disk from the good copies, checksums verified: no stale copy is left.
// A disk that came back (ADR-0027) is only checked by a scrub, and a resumed
// scrub may have read part of the pool before since: it does not count.
func checked(st *storage.PoolStatus, since int64) bool {
	scrubbed := st.Scrub.Status == "finished" && !st.Scrub.Resumed && st.Scrub.StartedAt >= since
	replaced := st.Replace != nil && st.Replace.State == "finished" && st.Replace.FinishedAt >= since
	return scrubbed || replaced
}

// Forget drops the marks of a pool that no longer exists.
func Forget(ctx context.Context, s Settings, pool string) error {
	if err := forget(ctx, s, settingKey, pool); err != nil {
		return err
	}
	if err := forget(ctx, s, repairKey, pool); err != nil {
		return err
	}
	mu.Lock()
	defer mu.Unlock()
	runs, err := loadRuns(ctx, s)
	if _, ok := runs[pool]; err != nil || !ok {
		return err
	}
	delete(runs, pool)
	return s.SetSetting(ctx, runKey, runs)
}

func forget(ctx context.Context, s Settings, key, pool string) error {
	mu.Lock()
	defer mu.Unlock()
	marks, err := load(ctx, s, key)
	if err != nil || marks[pool] == 0 {
		return err
	}
	delete(marks, pool)
	return s.SetSetting(ctx, key, marks)
}
