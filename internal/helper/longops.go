// SPDX-License-Identifier: AGPL-3.0-or-later

package helper

import (
	"context"
	"strings"
	"time"

	"github.com/crosswax/nasbuster/internal/apps"
)

// Long Btrfs operations (scrub, balance, replace) run in a transient unit of
// their own (ADR-0026): the helper exits when idle and is restarted by package
// upgrades, and systemd stops everything left in a stopped unit's cgroup. They
// run in the foreground there (-B, no --bg), so the unit lasts as long as the
// operation and keeps its output in the journal.

// btrfsBin is btrfs-progs' path on Debian; systemd-run needs it absolute.
const btrfsBin = "/usr/bin/btrfs"

// startWatch is how long a new unit is watched for an early failure (a bad
// target, another exclusive operation): after that, it is running.
const startWatch = 3 * time.Second

// startPoolJob runs cmd (after pre, ExecStartPre= lines) in the unit of job and
// reports a failure within startWatch with the unit's error line.
func (o *Ops) startPoolJob(ctx context.Context, job string, pre, cmd []string) *Error {
	return o.startPoolJobThen(ctx, job, pre, nil, cmd)
}

// startPoolJobThen is startPoolJob with ExecStartPost= lines: they run only if
// cmd succeeded.
func (o *Ops) startPoolJobThen(ctx context.Context, job string, pre, post, cmd []string) *Error {
	if err := o.startUnit(ctx, job, "CrossWax NasBuster "+job, "", nil, pre, post, cmd); err != nil {
		return err
	}
	const step = 250 * time.Millisecond
	for waited := time.Duration(0); waited < startWatch; waited += step {
		o.sleep(step)
		j := o.job(ctx, job, 5)
		switch j.State {
		case "failed":
			return errorf(CodeFailed, "%s: %s", job, jobError(j.Log))
		case "succeeded":
			return nil
		}
	}
	return nil
}

// startExclusivePoolJob is startPoolJob for a Btrfs exclusive operation: right
// after a replace, the kernel holds the previous one for a few more seconds.
func (o *Ops) startExclusivePoolJob(ctx context.Context, job string, cmd []string) *Error {
	for try := 0; ; try++ {
		err := o.startPoolJob(ctx, job, nil, cmd)
		if err == nil || !strings.Contains(err.Message, "another exclusive operation") {
			return err
		}
		if try == 14 {
			return errorf(CodePoolBusy, "another Btrfs operation is still finishing")
		}
		o.sleep(time.Second)
	}
}

// stopPoolJobs stops and forgets the long-operation units of a pool that goes
// away: they stay "active (exited)" after their job (seen on the Pi 5, after a
// pool was destroyed).
func (o *Ops) stopPoolJobs(ctx context.Context, pool string) {
	for _, kind := range []string{apps.ScrubJob, apps.BalanceJob, apps.ReplaceJob, apps.ResyncJob} {
		unit := apps.JobUnit(apps.PoolJob(kind, pool))
		o.try(ctx, "systemctl", "stop", unit)
		o.try(ctx, "systemctl", "reset-failed", unit)
	}
}

// jobError picks btrfs-progs' "ERROR: …" line from a unit's journal, else its
// last line: systemd's own "Failed with result" says less.
func jobError(log []string) string {
	for i := len(log) - 1; i >= 0; i-- {
		if strings.Contains(log[i], "ERROR") {
			return log[i]
		}
	}
	if len(log) > 0 {
		return log[len(log)-1]
	}
	return "failed (see its journal)"
}
