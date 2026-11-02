// SPDX-License-Identifier: AGPL-3.0-or-later

// Package scheduler runs the periodic protection tasks inside nasbusterd
// (ADR-0010): hourly share snapshots with retention, and periodic pool scrubs.
// No cron: the state lives in SQLite, missed runs are caught up after a reboot,
// every run is written to the activity log.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/crosswax/nasbuster/internal/alerts"
	"github.com/crosswax/nasbuster/internal/events"
	"github.com/crosswax/nasbuster/internal/helper"
	"github.com/crosswax/nasbuster/internal/snapshots"
	"github.com/crosswax/nasbuster/internal/store"
	"github.com/crosswax/nasbuster/internal/system"
)

// Settings keys.
const (
	ProtectionKey = "protection"
	StateKey      = "scheduler"
)

// Protection is the user-facing policy (API: /protection).
type Protection struct {
	Snapshots SnapshotSettings `json:"snapshots"`
	Scrub     ScrubSettings    `json:"scrub"`
}

// SnapshotSettings controls hourly share snapshots.
type SnapshotSettings struct {
	Enabled bool             `json:"enabled"`
	Policy  snapshots.Policy `json:"policy"`
}

// ScrubSettings controls periodic pool scrubs.
type ScrubSettings struct {
	Enabled      bool `json:"enabled"`
	IntervalDays int  `json:"intervalDays"`
}

// DefaultProtection is on by default: protection nobody configures is protection nobody has.
var DefaultProtection = Protection{
	Snapshots: SnapshotSettings{Enabled: true, Policy: snapshots.DefaultPolicy},
	Scrub:     ScrubSettings{Enabled: true, IntervalDays: 30},
}

// Valid checks bounds.
func (p Protection) Valid() bool {
	return p.Snapshots.Policy.Valid() && p.Scrub.IntervalDays >= 7 && p.Scrub.IntervalDays <= 365
}

// State remembers the last runs.
type State struct {
	LastSnapshotRun int64                  `json:"lastSnapshotRun"`
	LastScrub       map[string]int64       `json:"lastScrub"` // pool -> unix time
	LastAlertEval   int64                  `json:"lastAlertEval"`
	LastSmartRun    int64                  `json:"lastSmartRun"`
	Smart           map[string]SmartRecord `json:"smart"` // disk id -> last reads
	LastBackup      int64                  `json:"lastBackup"`
	LastRecycle     int64                  `json:"lastRecycle"`
	LastInitramfs   int64                  `json:"lastInitramfs"`
	InitramfsPools  string                 `json:"initramfsPools"` // pool names at the last check
	InitramfsWatch  bool                   `json:"initramfsWatch"` // a rebuild was under way: check every minute
}

// Scheduler runs the tasks.
type Scheduler struct {
	Store  *store.Store
	Helper helper.Client
	Prober *system.Prober // power and temperature alerts (optional)
	// RootSpace measures the system disk for its space alert (ADR-0034);
	// nil: none (demo, tests).
	RootSpace func() (used, size uint64, ok bool)
	Notifier  *alerts.Notifier // nil: alerts are recorded but not sent
	Events    *events.Recorder // event journal (ADR-0031); nil: no journal
	// ExportPath is where the database copy for configuration backups goes
	// ("" disables them).
	ExportPath string
	Logger     *slog.Logger
	Now        func() time.Time
	// Boot gives the current boot id: a parity repair does not survive a
	// reboot (nil: unknown, every pass counts as the same boot).
	Boot func() (id string, uptime time.Duration, ok bool)
}

func (s *Scheduler) bootID() string {
	if s.Boot == nil {
		return ""
	}
	id, _, _ := s.Boot()
	return id
}

// LoadProtection returns the stored policy or the defaults.
func LoadProtection(ctx context.Context, st *store.Store) (Protection, error) {
	p := DefaultProtection
	if err := st.GetSetting(ctx, ProtectionKey, &p); err != nil && !errors.Is(err, store.ErrNotFound) {
		return p, err
	}
	return p, nil
}

// LoadState returns the stored state (empty if none).
func LoadState(ctx context.Context, st *store.Store) (State, error) {
	s := State{LastScrub: map[string]int64{}}
	if err := st.GetSetting(ctx, StateKey, &s); err != nil && !errors.Is(err, store.ErrNotFound) {
		return s, err
	}
	if s.LastScrub == nil {
		s.LastScrub = map[string]int64{}
	}
	if s.Smart == nil {
		s.Smart = map[string]SmartRecord{}
	}
	return s, nil
}

// Run ticks every minute until ctx is done.
func (s *Scheduler) Run(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	s.Tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Tick(ctx)
		}
	}
}

// Report says what one tick did (tests).
type Report struct {
	Snapshots      int
	Pruned         int
	Scrubs         int
	AlertsRaised   int
	AlertsResolved int
	Restores       int // redundancy restores started
	Repairs        int // parity repairs started (ADR-0032)
	Backups        int // configuration backups made
	Errors         []string
}

// Tick runs whatever is due.
func (s *Scheduler) Tick(ctx context.Context) Report {
	var r Report
	prot, err := LoadProtection(ctx, s.Store)
	if err != nil {
		s.logger().Error("scheduler: load protection", "err", err)
		return r
	}
	state, err := LoadState(ctx, s.Store)
	if err != nil {
		s.logger().Error("scheduler: load state", "err", err)
		return r
	}
	now := s.now()
	ctx = helper.WithActor(ctx, "scheduler")
	// Times taken before the clock was synchronized are recomputed (ADR-0031).
	s.Events.Sync(ctx)

	// Snapshots: once per clock hour (catches up after downtime).
	last := time.Unix(state.LastSnapshotRun, 0)
	if prot.Snapshots.Enabled && now.Truncate(time.Hour).After(last.Truncate(time.Hour)) {
		if s.snapshots(ctx, prot.Snapshots.Policy, &r) > 0 {
			state.LastSnapshotRun = now.Unix() // no share yet: nothing ran
		}
		if r.Snapshots > 0 || len(r.Errors) > 0 {
			s.audit(ctx, "snapshot.auto", "", result(r.Errors), fmt.Sprintf("%d snapshots, %d pruned", r.Snapshots, r.Pruned))
		}
	}

	// Scrubs: per pool, every IntervalDays since the last one (or creation).
	if prot.Scrub.Enabled {
		pools, err := s.Store.Pools(ctx)
		if err != nil {
			r.Errors = append(r.Errors, err.Error())
		}
		interval := time.Duration(prot.Scrub.IntervalDays) * 24 * time.Hour
		for _, p := range pools {
			lastScrub := state.LastScrub[p.Name]
			if lastScrub == 0 {
				lastScrub = p.CreatedAt
			}
			if now.Sub(time.Unix(lastScrub, 0)) < interval {
				continue
			}
			err := s.Helper.Call(ctx, helper.OpPoolScrub, helper.PoolArgs{Name: p.Name}, nil)
			state.LastScrub[p.Name] = now.Unix() // also on failure: retry next interval, not every minute
			if err != nil {
				r.Errors = append(r.Errors, p.Name+": "+err.Error())
				s.audit(ctx, "pool.scrub", p.Name, "error", err.Error())
				continue
			}
			r.Scrubs++
			s.audit(ctx, "pool.scrub", p.Name, "ok", "scheduled")
		}
	}

	// Alerts every 5 minutes.
	if now.Sub(time.Unix(state.LastAlertEval, 0)) >= alertEvery {
		s.evaluateAlerts(ctx, &state, &r)
		state.LastAlertEval = now.Unix()
	}

	// Recycle bins: once a day, forget what was deleted long enough ago.
	if now.Sub(time.Unix(state.LastRecycle, 0)) >= 24*time.Hour {
		state.LastRecycle = now.Unix()
		if all, err := s.Store.Shares(ctx); err == nil {
			for _, sh := range all {
				if !sh.Recycle {
					continue
				}
				args := helper.RecyclePurgeArgs{Pool: sh.Pool, Share: sh.Name, Days: sh.RecycleDays}
				if err := s.Helper.Call(ctx, helper.OpRecyclePurge, args, nil); err != nil {
					r.Errors = append(r.Errors, sh.Name+": "+err.Error())
				}
			}
		}
		// The data of removed applications, kept a week (ADR-0035).
		if pools, err := s.Store.Pools(ctx); err == nil {
			for _, p := range pools {
				if err := s.Helper.Call(ctx, helper.OpAppTrashPurge, helper.TrashPurgeArgs{Pool: p.Name}, nil); err != nil {
					r.Errors = append(r.Errors, p.Name+": app trash: "+err.Error())
				}
			}
		}
	}

	// Configuration backup on the pools, once a day (ADR-0014). Not of an
	// empty configuration while the onboarding runs (P6).
	if s.ExportPath != "" && now.Sub(time.Unix(state.LastBackup, 0)) >= 24*time.Hour && s.worthBackingUp(ctx) {
		if res, err := s.Backup(ctx); err != nil {
			r.Errors = append(r.Errors, "config backup: "+err.Error())
			s.audit(ctx, "config.backup", "", "error", err.Error())
			state.LastBackup = now.Add(-23 * time.Hour).Unix() // retry in an hour
		} else if len(res.Pools) > 0 {
			r.Backups++
			state.LastBackup = now.Unix()
			s.audit(ctx, "config.backup", res.Name, "ok", strings.Join(res.Pools, ", "))
		}
	}

	s.initramfs(ctx, &state, now, &r)

	if err := s.Store.SetSetting(ctx, StateKey, state); err != nil {
		s.logger().Error("scheduler: save state", "err", err)
	}
	return r
}

// initramfs keeps the initramfs in step with mdadm.conf (ADR-0033). Pool
// operations start the rebuild themselves; this retries a failed one (every
// hour), and follows a rebuild minute by minute until it is seen done: a
// change made while one ran (a second pool created a few seconds later) is
// then rebuilt within minutes, not an hour. A change of the pools list starts
// the watch too.
func (s *Scheduler) initramfs(ctx context.Context, state *State, now time.Time, r *Report) {
	pools := state.InitramfsPools
	if all, err := s.Store.Pools(ctx); err == nil {
		names := make([]string, 0, len(all))
		for _, p := range all {
			names = append(names, p.Name)
		}
		pools = strings.Join(names, ",")
	}
	if !state.InitramfsWatch && pools == state.InitramfsPools && now.Sub(time.Unix(state.LastInitramfs, 0)) < time.Hour {
		return
	}
	state.LastInitramfs, state.InitramfsPools, state.InitramfsWatch = now.Unix(), pools, false
	var res helper.InitramfsResult
	if err := s.Helper.Call(ctx, helper.OpInitramfsRefresh, struct{}{}, &res); err != nil {
		r.Errors = append(r.Errors, "initramfs: "+err.Error())
		return
	}
	state.InitramfsWatch = res.State == "running" || res.State == "started"
	if res.LastError != "" {
		r.Errors = append(r.Errors, "initramfs: "+res.LastError)
		s.audit(ctx, "initramfs.update", "", "error", res.LastError)
	}
	if res.State == "started" {
		s.audit(ctx, "initramfs.update", "", "ok", "rebuild started: mdadm.conf changed")
	}
}

// snapshots returns how many shares it went through.
func (s *Scheduler) snapshots(ctx context.Context, policy snapshots.Policy, r *Report) int {
	shares, err := s.Store.Shares(ctx)
	if err != nil {
		r.Errors = append(r.Errors, err.Error())
		return 0
	}
	for _, sh := range shares {
		if sh.TimeMachine {
			continue // Time Machine keeps its own versions; snapshots of its bands would fill the pool
		}
		args := helper.SnapshotArgs{Pool: sh.Pool, Share: sh.Name}
		if err := s.Helper.Call(ctx, helper.OpSnapshotCreate, args, nil); err != nil {
			r.Errors = append(r.Errors, sh.Name+": "+err.Error())
			continue
		}
		r.Snapshots++
		var list helper.SnapshotListResult
		if err := s.Helper.Call(ctx, helper.OpSnapshotList, args, &list); err != nil {
			r.Errors = append(r.Errors, sh.Name+": "+err.Error())
			continue
		}
		for _, name := range snapshots.Prune(list.Snapshots, policy) {
			del := helper.SnapshotArgs{Pool: sh.Pool, Share: sh.Name, Name: name}
			if err := s.Helper.Call(ctx, helper.OpSnapshotDelete, del, nil); err != nil {
				r.Errors = append(r.Errors, sh.Name+"/"+name+": "+err.Error())
				continue
			}
			r.Pruned++
		}
	}
	return len(shares)
}

func (s *Scheduler) audit(ctx context.Context, action, target, res, detail string) {
	_ = s.Store.Audit(ctx, store.AuditEntry{Actor: "scheduler", Action: action, Target: target, Result: res, Detail: detail})
}

func result(errs []string) string {
	if len(errs) > 0 {
		return "error"
	}
	return "ok"
}

func (s *Scheduler) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Scheduler) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

// Backup exports the database and has the helper store it on every mounted pool.
func (s *Scheduler) Backup(ctx context.Context) (helper.BackupResult, error) {
	var res helper.BackupResult
	args, err := BackupArgs(ctx, s.Store)
	if err != nil {
		return res, fmt.Errorf("describe the configuration: %w", err)
	}
	if err := s.Store.ExportTo(ctx, s.ExportPath); err != nil {
		return res, fmt.Errorf("export: %w", err)
	}
	err = s.Helper.Call(ctx, helper.OpConfigBackup, args, &res)
	return res, err
}

// BackupArgs describes the configuration for the backup manifest (P6): the
// installation that makes it, its admin, its shares.
func BackupArgs(ctx context.Context, st *store.Store) (helper.BackupArgs, error) {
	id, err := st.InstallationID(ctx)
	if err != nil {
		return helper.BackupArgs{}, err
	}
	args := helper.BackupArgs{Installation: id, Shares: []string{}}
	admins, err := st.Admins(ctx)
	if err != nil {
		return args, err
	}
	if len(admins) > 0 {
		args.Admin = admins[0].Username
	}
	shares, err := st.Shares(ctx)
	if err != nil {
		return args, err
	}
	for _, sh := range shares {
		args.Shares = append(args.Shares, sh.Name)
	}
	return args, nil
}

// worthBackingUp is false for a configuration with no NAS user and no share
// while the onboarding is not finished (P6): on a fresh SD card, the pools
// just imported got backups with nobody in them, listed first, which a
// hurried restore would have picked. Any doubt backs up.
func (s *Scheduler) worthBackingUp(ctx context.Context) bool {
	var onboarding struct {
		Completed bool `json:"completed"`
	}
	if err := s.Store.GetSetting(ctx, "onboarding", &onboarding); err == nil && onboarding.Completed {
		return true
	}
	users, err := s.Store.NASUsers(ctx)
	if err != nil {
		return true
	}
	shares, err := s.Store.Shares(ctx)
	if err != nil {
		return true
	}
	return len(users) > 0 || len(shares) > 0
}
