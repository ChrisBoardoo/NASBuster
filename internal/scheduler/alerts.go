// SPDX-License-Identifier: AGPL-3.0-or-later

package scheduler

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/crosswax/nasbuster/internal/alerts"
	"github.com/crosswax/nasbuster/internal/helper"
	"github.com/crosswax/nasbuster/internal/resync"
	"github.com/crosswax/nasbuster/internal/storage"
	"github.com/crosswax/nasbuster/internal/store"
	"github.com/crosswax/nasbuster/internal/system"
)

// NotificationsKey stores the notification channels (with secrets).
const NotificationsKey = "notifications"

// Alert evaluation and SMART read periods (ADR-0011).
const (
	alertEvery = 5 * time.Minute
	smartEvery = 24 * time.Hour
)

// SmartRecord keeps the last SMART read of a disk and the one before.
type SmartRecord struct {
	Current  storage.SmartReport  `json:"current"`
	Previous *storage.SmartReport `json:"previous,omitempty"`
	At       int64                `json:"at"`
}

// LoadChannels returns the notification channels (empty if never set).
func LoadChannels(ctx context.Context, st *store.Store) (alerts.Channels, error) {
	var ch alerts.Channels
	if err := st.GetSetting(ctx, NotificationsKey, &ch); err != nil && !errors.Is(err, store.ErrNotFound) {
		return ch, err
	}
	return ch, nil
}

// evaluateAlerts raises, keeps and resolves alerts, and notifies changes.
func (s *Scheduler) evaluateAlerts(ctx context.Context, state *State, r *Report) {
	now := s.now()
	pools, err := s.Store.Pools(ctx)
	if err != nil {
		r.Errors = append(r.Errors, err.Error())
		return
	}
	var poolInputs []alerts.PoolInput
	var members []string
	boot := s.bootID()
	for _, p := range pools {
		in := alerts.PoolInput{Name: p.Name}
		var st storage.PoolStatus
		if err := s.Helper.Call(ctx, helper.OpPoolStatus, helper.PoolArgs{Name: p.Name}, &st); err == nil {
			if err := resync.Apply(ctx, s.Store, &st, now, boot); err != nil {
				r.Errors = append(r.Errors, p.Name+": "+err.Error())
			}
			in.Status = &st
			s.restoreRedundancy(ctx, p.Name, st, r)
			s.resumeScrub(ctx, p.Name, st, r)
			if !s.repairParity(ctx, p.Name, st, r) {
				s.resyncScrub(ctx, p.Name, st, r)
			}
		}
		poolInputs = append(poolInputs, in)
		members = append(members, p.Members...)
	}

	// SMART once a day: often enough for a NAS, without waking disks every 5 minutes.
	if now.Sub(time.Unix(state.LastSmartRun, 0)) >= smartEvery && len(members) > 0 {
		for _, id := range members {
			var rep storage.SmartReport
			if err := s.Helper.Call(ctx, helper.OpDiskSmart, helper.DiskSmartArgs{ID: id}, &rep); err != nil {
				continue
			}
			rec := SmartRecord{Current: rep, At: now.Unix()}
			if old, ok := state.Smart[id]; ok {
				prev := old.Current
				rec.Previous = &prev
			}
			state.Smart[id] = rec
		}
		state.LastSmartRun = now.Unix()
	}
	var smartInputs []alerts.SmartInput
	for _, id := range members {
		if rec, ok := state.Smart[id]; ok {
			smartInputs = append(smartInputs, alerts.SmartInput{DiskID: id, Current: rec.Current, Previous: rec.Previous})
		}
	}

	var checks []system.Check
	nas := "nasbuster"
	if s.Prober != nil {
		checks = s.Prober.Checks(ctx)
		if h := s.Prober.Info().Hostname; h != "" {
			nas = h
		}
	}

	active, err := s.Store.ActiveAlerts(ctx)
	if err != nil {
		r.Errors = append(r.Errors, err.Error())
		return
	}
	holding := alerts.Evaluate(poolInputs, checks, smartInputs)
	// Space (ADR-0034): the mounted pools, as the user counts them, and the
	// system disk. The active alerts give the hysteresis.
	var space []alerts.SpaceInput
	for _, in := range poolInputs {
		if st := in.Status; st != nil && st.Mounted {
			space = append(space, alerts.SpaceInput{Pool: in.Name, UsedBytes: st.LogicalUsedBytes, SizeBytes: st.UsableBytes})
		}
	}
	if s.RootSpace != nil {
		if used, size, ok := s.RootSpace(); ok {
			space = append(space, alerts.SpaceInput{UsedBytes: used, SizeBytes: size})
		}
	}
	holding = append(holding, alerts.EvaluateSpace(space, active)...)
	sort.Slice(holding, func(i, j int) bool { return holding[i].ID < holding[j].ID })
	diff := alerts.Diff(active, holding)
	lang := s.Store.AdminLanguage(ctx)
	channels, _ := LoadChannels(ctx, s.Store)

	raised := map[string]bool{}
	for _, c := range diff.Raised {
		raised[c.ID] = true
		if err := s.Store.RaiseAlert(ctx, c.ID, c.Level, c.Code, c.Params); err != nil {
			r.Errors = append(r.Errors, err.Error())
			continue
		}
		r.AlertsRaised++
		s.Events.AlertRaised(ctx, c.Level, c.Code, c.Params)
		pub := alerts.Public(c.Params) // no full disk serial leaves the NAS
		s.notify(ctx, channels, alerts.Message{NAS: nas, Title: alerts.Title(c.Code, lang, pub),
			Level: c.Level, Code: c.Code, Params: pub, Time: now.Unix()})
		s.audit(ctx, "alert.raised", c.ID, c.Level, "")
	}
	for _, c := range holding {
		if !raised[c.ID] {
			_ = s.Store.TouchAlert(ctx, c.ID)
		}
	}
	for _, id := range diff.Resolved {
		a, err := s.Store.ResolveAlert(ctx, id)
		if err != nil {
			continue
		}
		r.AlertsResolved++
		s.Events.AlertResolved(ctx, a)
		pub := alerts.Public(a.Params)
		s.notify(ctx, channels, alerts.Message{NAS: nas, Title: alerts.ResolvedTitle(a.Code, lang, pub),
			Level: a.Level, Code: a.Code, Params: pub, Resolved: true, Time: now.Unix()})
		s.audit(ctx, "alert.resolved", id, "ok", "")
	}
}

func (s *Scheduler) notify(ctx context.Context, ch alerts.Channels, m alerts.Message) {
	if s.Notifier == nil {
		return
	}
	for _, res := range s.Notifier.Send(ctx, ch, m) {
		if !res.OK {
			s.logger().Warn("notification failed", "channel", res.Channel, "err", res.Error)
		}
	}
}

// growSlack: below this, unused partition space is alignment, not a bigger disk.
const growSlack = 64 << 20

// restoreRedundancy converts the chunks written while degraded back to the
// pool's profiles (ADR-0012), and grows the disks that a bigger one replaced:
// the restore resizes every device first. The only automatic repair: it copies
// blocks to the right profile and deletes nothing. It needs every disk present
// and no replace or balance running.
func (s *Scheduler) restoreRedundancy(ctx context.Context, pool string, st storage.PoolStatus, r *Report) {
	grow := st.Slack >= growSlack
	if !st.Mounted || (!st.MixedProfiles && !grow) || st.MissingDevices > 0 || st.Balance != nil ||
		(st.Replace != nil && st.Replace.State == "running") {
		return
	}
	var res helper.RestoreResult
	if err := s.Helper.Call(ctx, helper.OpPoolRestore, helper.PoolArgs{Name: pool}, &res); err != nil {
		r.Errors = append(r.Errors, pool+": "+err.Error())
		s.audit(ctx, "pool.restore_redundancy", pool, "error", err.Error())
		return
	}
	if res.BalanceStarted {
		r.Restores++
		s.audit(ctx, "pool.restore_redundancy", pool, "ok", "automatic")
	} else if grow {
		s.audit(ctx, "pool.grow", pool, "ok", "automatic")
	}
}

// resumeScrub goes on with a scrub that a reboot cut short ("interrupted"):
// the monthly scrub of a big pool takes hours, and starting over would wait
// for the next month. A scrub the user stopped ("aborted") is left alone.
func (s *Scheduler) resumeScrub(ctx context.Context, pool string, st storage.PoolStatus, r *Report) {
	if st.Scrub.Status != "interrupted" || !st.Mounted || st.MissingDevices > 0 || st.Balance != nil ||
		(st.Replace != nil && st.Replace.State == "running") {
		return
	}
	err := s.Helper.Call(ctx, helper.OpPoolScrubResume, helper.PoolArgs{Name: pool}, nil)
	var he *helper.Error
	if errors.As(err, &he) && he.Code == helper.CodePoolBusy {
		return
	}
	if err != nil {
		r.Errors = append(r.Errors, pool+": "+err.Error())
		s.audit(ctx, "pool.scrub", pool, "error", err.Error())
		return
	}
	r.Scrubs++
	s.audit(ctx, "pool.scrub", pool, "ok", "resumed")
}

// repairParity starts the parity repair a pool owes after its array went
// beyond its tolerance (ADR-0032), once md has every member in sync: a
// rebuild of a member taken back may come first, for hours, across reboots.
// It starts it again when one did not reach its end (resync.Apply ended the
// run: a reboot, a member that dropped, a stop halfway). It reports whether
// the repair is still owed: the scrub waits until it is done.
func (s *Scheduler) repairParity(ctx context.Context, pool string, st storage.PoolStatus, r *Report) bool {
	if !st.RepairNeeded {
		return false
	}
	if !st.Mounted || st.MD == nil || st.MD.Degraded() || st.MD.Sync != nil {
		return true // not back yet, a member missing, or md at work (the repair, a rebuild)
	}
	err := s.Helper.Call(ctx, helper.OpPoolRepairParity, helper.PoolArgs{Name: pool}, nil)
	var he *helper.Error
	if errors.As(err, &he) && he.Code == helper.CodePoolBusy {
		return true
	}
	if err != nil {
		r.Errors = append(r.Errors, pool+": "+err.Error())
		s.audit(ctx, "pool.repair_parity", pool, "error", err.Error())
		return true
	}
	if err := resync.RepairStarted(ctx, s.Store, pool, s.now(), s.bootID()); err != nil {
		r.Errors = append(r.Errors, pool+": "+err.Error())
	}
	r.Repairs++
	s.audit(ctx, "pool.repair_parity", pool, "ok", "")
	return true
}

// resyncScrub checks both copies after a degraded period (ADR-0027). Like the
// restore, it only rewrites a bad copy from a good one. It waits until every
// disk is in use, the single chunks are converted and nothing else runs.
func (s *Scheduler) resyncScrub(ctx context.Context, pool string, st storage.PoolStatus, r *Report) {
	if !st.ResyncNeeded || !st.Mounted || st.MissingDevices > 0 || st.MixedProfiles || st.Balance != nil ||
		(st.MD != nil && st.MD.Sync != nil) || // md rebuilds a member: the scrub comes after

		(st.Replace != nil && st.Replace.State == "running") || st.Scrub.Status == "running" ||
		// Stopped by the user: they start it again. Cut by a reboot: resumeScrub goes on with it.
		st.Scrub.Status == "aborted" || st.Scrub.Status == "interrupted" {
		return
	}
	err := s.Helper.Call(ctx, helper.OpPoolScrub, helper.PoolArgs{Name: pool}, nil)
	var he *helper.Error
	if errors.As(err, &he) && he.Code == helper.CodePoolBusy {
		return // the resync job of a resume is at work
	}
	if err != nil {
		r.Errors = append(r.Errors, pool+": "+err.Error())
		s.audit(ctx, "pool.scrub", pool, "error", err.Error())
		return
	}
	r.Scrubs++
	s.audit(ctx, "pool.scrub", pool, "ok", "resync")
}
