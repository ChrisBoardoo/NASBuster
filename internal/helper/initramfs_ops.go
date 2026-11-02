// SPDX-License-Identifier: AGPL-3.0-or-later

package helper

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"time"

	"github.com/crosswax/nasbuster/internal/apps"
)

// The initramfs assembles md arrays at boot with its own copy of mdadm.conf
// (Raspberry Pi OS ships mdadm in it): a copy older than a pool names its array
// after the superblock ("rpi5-nas:cwnsb-e2", seen on the Pi 5) and mdadm
// --monitor loses it. Debian asks for "update-initramfs -u" after every change
// of mdadm.conf, at the top of the file itself, and OMV does it whenever an md
// device is created, changed or deleted (ADR-0033). Raspberry Pi OS's
// update-initramfs rebuilds every flavour of the latest kernel on -u
// (initramfs8 and initramfs_2712).
const updateInitramfsBin = "/usr/sbin/update-initramfs"

// What the initramfs was last built with: the copy of mdadm.conf taken before
// a rebuild (pending) becomes the stamp when the rebuild succeeds, moved by the
// unit itself (ExecStartPost= runs only after a success), so a reboot right
// after it does not make its outcome unknown (seen on the Pi 5: one rebuild
// too many). A copy taken before the rebuild reads the file can only be older
// than what the initramfs holds: the error is one rebuild too many, never one
// too few.
const (
	initramfsStateDir = "/var/lib/cwnsb-helper"
	initramfsStamp    = initramfsStateDir + "/initramfs-mdadm.conf"
	initramfsPending  = initramfsStamp + ".pending"
	initramfsRecord   = "/usr/bin/mv -f " + initramfsPending + " " + initramfsStamp
)

// initramfsProbe bounds one refresh: a hung systemctl (a busy D-Bus) must
// not hold initramfsMu, and a pool operation's reply, forever.
const initramfsProbe = time.Minute

// initramfsRefresh rebuilds the initramfs when mdadm.conf differs from the
// copy it was last built with. It is cheap when nothing changed: the daemon
// calls it at startup and the scheduler every hour (every minute while a
// rebuild runs, or after the pools changed), which also retries a failed
// rebuild; pool operations that write mdadm.conf call it right after.
// Before any stamp, only a mdadm.conf holding a NasBuster pool counts as
// changed: a system without parity pools is never rebuilt for nothing.
func (o *Ops) initramfsRefresh(ctx context.Context) (InitramfsResult, *Error) {
	o.initramfsMu.Lock()
	defer o.initramfsMu.Unlock()
	if !o.Files.Exists(updateInitramfsBin) {
		return InitramfsResult{State: "unsupported"}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, initramfsProbe)
	defer cancel()
	// A probe that fails says nothing: it is never taken for a reboot, which
	// would stop a rebuild under way.
	state, err := o.jobState(ctx, apps.InitramfsJob)
	if err != nil {
		return InitramfsResult{}, err
	}
	if state == "running" {
		return InitramfsResult{State: "running"}, nil
	}
	var failed string
	var failedConf []byte
	if pending, err := o.Files.ReadFile(initramfsPending); err == nil {
		switch state {
		case "succeeded": // the unit recorded it; done here too if it could not
			if err := o.Files.WriteFile(initramfsStamp, pending, 0o600); err != nil {
				return InitramfsResult{}, errorf(CodeFailed, "write %s: %v", initramfsStamp, err)
			}
		case "failed":
			failed, failedConf = jobError(o.job(ctx, apps.InitramfsJob, 5).Log), pending
			o.logger().Warn("initramfs rebuild failed", "err", failed)
		}
		// "none": the unit is gone (a reboot), its outcome unknown: rebuild again.
		_ = o.Files.Remove(initramfsPending)
	}

	conf, cerr := o.mdConfFixed()
	if cerr != nil {
		return InitramfsResult{}, cerr
	}
	stamp, rerr := o.Files.ReadFile(initramfsStamp)
	switch {
	case rerr == nil:
		if string(stamp) == string(conf) {
			return InitramfsResult{State: "current", LastError: failed}, nil
		}
	case errors.Is(rerr, fs.ErrNotExist):
		if !strings.Contains(string(conf), mdConfMarkerPrefix) {
			return InitramfsResult{State: "current", LastError: failed}, nil
		}
	default:
		return InitramfsResult{}, errorf(CodeFailed, "read %s: %v", initramfsStamp, rerr)
	}
	if failed != "" && string(conf) == string(failedConf) {
		// The same file failed just now: retried at the next call, not in a loop.
		return InitramfsResult{State: "failed", LastError: failed}, nil
	}

	// The previous run's unit goes BEFORE the new copy is written: a helper
	// killed between the two would otherwise leave an old success next to a
	// new copy, recorded as built at the next call.
	o.forgetUnit(ctx, apps.JobUnit(apps.InitramfsJob))
	if err := o.Files.MkdirAll(initramfsStateDir, 0o700); err != nil {
		return InitramfsResult{}, errorf(CodeFailed, "create %s: %v", initramfsStateDir, err)
	}
	if err := o.Files.WriteFile(initramfsPending, conf, 0o600); err != nil {
		return InitramfsResult{}, errorf(CodeFailed, "write %s: %v", initramfsPending, err)
	}
	if err := o.startJob(ctx, apps.InitramfsJob, "", nil, nil, []string{initramfsRecord}, []string{updateInitramfsBin, "-u"}); err != nil {
		_ = o.Files.Remove(initramfsPending)
		return InitramfsResult{}, err
	}
	o.logger().Info("initramfs rebuild started: mdadm.conf changed")
	return InitramfsResult{State: "started", LastError: failed}, nil
}

// mdConfFixed reads mdadm.conf, and rewrites it first, under the lock of
// every mdadm.conf writer, if one of our blocks still carries "spares=" or
// another ARRAY line names the array of one of our blocks (Debian's mdadm
// package, installed or reconfigured after the pool was imported or created).
func (o *Ops) mdConfFixed() ([]byte, *Error) {
	o.mdConfMu.Lock()
	defer o.mdConfMu.Unlock()
	conf, err := o.Files.ReadFile(o.MDConf)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, errorf(CodeFailed, "read %s: %v", o.MDConf, err)
	}
	fixed := blocksWithoutSpares(string(conf))
	fixed, disabled := foreignDuplicatesDisabled(fixed)
	if fixed != string(conf) {
		if err := o.Files.WriteFile(o.MDConf, []byte(fixed), 0o644); err != nil {
			return nil, errorf(CodeFailed, "write %s: %v", o.MDConf, err)
		}
		o.logger().Info("mdadm.conf fixed: spares= dropped from NasBuster's ARRAY lines, other lines for its arrays commented out", "pools", disabled)
		conf = []byte(fixed)
	}
	return conf, nil
}

// blocksWithoutSpares drops "spares=N" from the ARRAY lines of NasBuster's
// blocks: pools created before the fix carry "spares=1" (seen on the Pi 5),
// and mdadm --monitor would report the missing spare forever. Lines outside
// our blocks belong to the administrator and stay as they are.
func blocksWithoutSpares(conf string) string {
	lines := strings.Split(conf, "\n")
	inBlock := false
	for i, line := range lines {
		switch {
		case strings.HasPrefix(line, mdConfMarkerPrefix):
			inBlock = true
		case strings.HasPrefix(line, mdConfEndPrefix):
			inBlock = false
		case inBlock && strings.HasPrefix(line, "ARRAY ") && strings.Contains(line, "spares="):
			lines[i] = withoutSpares(line)
		}
	}
	return strings.Join(lines, "\n")
}

// afterMDConfChange starts the initramfs rebuild after an operation that may
// have written mdadm.conf. A failure never undoes the operation: it is logged,
// and the next call retries.
func (o *Ops) afterMDConfChange(ctx context.Context) {
	if _, err := o.initramfsRefresh(context.WithoutCancel(ctx)); err != nil { // bounded by initramfsProbe
		o.logger().Warn("could not start the initramfs rebuild, retried later", "err", err.Message)
	}
}
