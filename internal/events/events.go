// SPDX-License-Identifier: AGPL-3.0-or-later

// Package events writes the event journal (ADR-0031): what happened, when, to
// what, with a code that the UI explains in its lexicon. The alerts are the
// current state; the journal is the history, and it survives reboots.
//
// A Pi has no battery-backed clock: after a power cut without network, the
// time is wrong until NTP sets it. An event recorded meanwhile is marked
// "uncertain" with its time since boot, and recomputed once the clock is
// synchronized, as long as it is the same boot.
package events

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/crosswax/nasbuster/internal/store"
)

// Sources.
const (
	SourceAlert  = "alert"
	SourceSystem = "system"
)

// Caps: the journal stays small on the SD card.
const (
	keepEvents = 5000
	keepFor    = 365 * 24 * time.Hour
	pruneEvery = 50 // inserts between two prunes
)

// SettingsKey stores the journal settings.
const SettingsKey = "journal"

// Settings: the system journal kept across reboots, on by default (the
// administrator's choice, ADR-0031). The helper applies it at every start.
type Settings struct {
	Persistent bool `json:"persistent"`
}

// LoadSettings returns the stored settings, or the defaults.
func LoadSettings(ctx context.Context, st *store.Store) (Settings, error) {
	s := Settings{Persistent: true}
	if err := st.GetSetting(ctx, SettingsKey, &s); err != nil && !errors.Is(err, store.ErrNotFound) {
		return s, err
	}
	return s, nil
}

// bootKey remembers the boot the daemon last ran in, and whether it stopped
// cleanly before that boot ended.
const bootKey = "events.boot"

type bootState struct {
	BootID    string `json:"bootId"`
	CleanStop bool   `json:"cleanStop"`
}

// Recorder writes events. A nil Recorder records nothing.
type Recorder struct {
	Store  *store.Store
	Now    func() time.Time
	Synced func() (synced, known bool)                       // nil: assumed synchronized
	Boot   func() (id string, uptime time.Duration, ok bool) // nil: times cannot be corrected
	Logger *slog.Logger

	mu    sync.Mutex
	added int
}

// Record writes one event; its time, time state and boot are filled here.
func (r *Recorder) Record(ctx context.Context, e store.Event) {
	r.record(ctx, e, 0)
}

// record writes an event that happened `ago` before now (the boot happened
// before the daemon started: up to the whole uptime).
func (r *Recorder) record(ctx context.Context, e store.Event, ago time.Duration) {
	if r == nil || r.Store == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	e.Time, e.TimeState = now.Add(-ago).Unix(), store.TimeExact
	synced, known := r.synced()
	if known && !synced {
		e.TimeState = store.TimeUncertain
	}
	if id, up, ok := r.boot(); ok {
		e.BootID, e.UptimeMs = id, (up - ago).Milliseconds()
		if synced {
			r.correct(ctx, id, now, up) // the earlier entries of this boot first
		}
	}
	if _, err := r.Store.AddEvent(ctx, e); err != nil {
		r.logger().Error("event journal: cannot record", "code", e.Code, "err", err)
		return
	}
	r.added++
	if r.added%pruneEvery == 0 {
		r.prune(ctx, now)
	}
}

// Sync recomputes the uncertain times of this boot once the clock is
// synchronized. The scheduler calls it every minute: it only reads when there
// is nothing to correct.
func (r *Recorder) Sync(ctx context.Context) {
	if r == nil || r.Store == nil {
		return
	}
	if synced, _ := r.synced(); !synced {
		return
	}
	id, up, ok := r.boot()
	if !ok {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.correct(ctx, id, r.now(), up)
}

func (r *Recorder) correct(ctx context.Context, bootID string, now time.Time, up time.Duration) {
	has, err := r.Store.HasUncertainEvents(ctx, bootID)
	if err != nil || !has {
		return
	}
	n, err := r.Store.CorrectEventTimes(ctx, bootID, now.UnixMilli()-up.Milliseconds())
	if err != nil {
		r.logger().Error("event journal: cannot correct times", "err", err)
		return
	}
	r.logger().Info("event journal: times recomputed after the clock synchronized", "events", n)
}

func (r *Recorder) prune(ctx context.Context, now time.Time) {
	if err := r.Store.PruneEvents(ctx, keepEvents, now.Add(-keepFor).Unix()); err != nil {
		r.logger().Error("event journal: cannot prune", "err", err)
	}
}

// Start runs when the daemon starts. In a new boot, it records the boot, and
// before it an unclean shutdown if the previous boot ended without the daemon
// being stopped: a power cut, an under-voltage, a freeze. lastAlive is when
// the previous run was last seen working (unix), 0 if unknown.
func (r *Recorder) Start(ctx context.Context, lastAlive int64) {
	if r == nil || r.Store == nil {
		return
	}
	id, up, ok := r.boot()
	if !ok {
		return
	}
	var prev bootState
	if err := r.Store.GetSetting(ctx, bootKey, &prev); err != nil && !errors.Is(err, store.ErrNotFound) {
		r.logger().Error("event journal: cannot read the previous boot", "err", err)
		return
	}
	if prev.BootID != id {
		// Both are dated at the boot, which may be long before the daemon
		// starts (first install): the stop came before it, the boot listed after.
		if prev.BootID != "" && !prev.CleanStop {
			params := map[string]any{}
			if lastAlive > 0 {
				params["lastAlive"] = lastAlive
			}
			r.record(ctx, store.Event{Level: "error", Code: "system.unclean_shutdown", Subject: "system", Source: SourceSystem, Params: params}, up)
		}
		r.record(ctx, store.Event{Level: "info", Code: "system.boot", Subject: "system", Source: SourceSystem}, up)
		r.mu.Lock()
		r.prune(ctx, r.now())
		r.mu.Unlock()
	}
	// Running in this boot (again, after an update): not a clean stop yet.
	if prev.BootID != id || prev.CleanStop {
		if err := r.Store.SetSetting(ctx, bootKey, bootState{BootID: id}); err != nil {
			r.logger().Error("event journal: cannot record the boot", "err", err)
		}
	}
}

// Stop runs when the daemon stops cleanly: at a shutdown or reboot, or when
// the daemon alone is restarted (then Start clears it again).
func (r *Recorder) Stop(ctx context.Context) {
	if r == nil || r.Store == nil {
		return
	}
	id, _, ok := r.boot()
	if !ok {
		return
	}
	if err := r.Store.SetSetting(ctx, bootKey, bootState{BootID: id, CleanStop: true}); err != nil {
		r.logger().Error("event journal: cannot record the clean stop", "err", err)
	}
}

// AlertRaised records an alert that appears, or gets worse.
func (r *Recorder) AlertRaised(ctx context.Context, level, code string, params map[string]any) {
	r.Record(ctx, store.Event{Level: level, Code: code, Phase: "raised", Subject: Subject(params), Source: SourceAlert, Params: params})
}

// AlertResolved records an alert that no longer holds.
func (r *Recorder) AlertResolved(ctx context.Context, a store.Alert) {
	r.Record(ctx, store.Event{Level: "info", Code: a.Code, Phase: "resolved", Subject: Subject(a.Params), Source: SourceAlert,
		Params: a.Params})
}

// Subject names what an event is about, from its params.
func Subject(params map[string]any) string {
	if p, ok := params["pool"].(string); ok && p != "" {
		return "pool:" + p
	}
	if d, ok := params["disk"].(string); ok && d != "" {
		return "disk:" + d
	}
	return "system"
}

func (r *Recorder) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Recorder) synced() (bool, bool) {
	if r.Synced == nil {
		return true, true
	}
	return r.Synced()
}

func (r *Recorder) boot() (string, time.Duration, bool) {
	if r.Boot == nil {
		return "", 0, false
	}
	return r.Boot()
}

func (r *Recorder) logger() *slog.Logger {
	if r.Logger != nil {
		return r.Logger
	}
	return slog.Default()
}
