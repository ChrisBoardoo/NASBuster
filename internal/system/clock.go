// SPDX-License-Identifier: AGPL-3.0-or-later

package system

import (
	"errors"
	"io/fs"
	"strconv"
	"strings"
	"time"
)

// BootInfo reads the kernel's id of the current boot and the time since boot
// (proc/uptime, which counts suspended time too). The event journal keeps both,
// to recompute a time taken before the clock was synchronized (ADR-0031).
func BootInfo(root fs.FS) func() (id string, uptime time.Duration, ok bool) {
	return func() (string, time.Duration, bool) {
		b, err := fs.ReadFile(root, "proc/sys/kernel/random/boot_id")
		if err != nil {
			return "", 0, false
		}
		id := strings.TrimSpace(string(b))
		u, err := fs.ReadFile(root, "proc/uptime")
		if err != nil || id == "" {
			return "", 0, false
		}
		fields := strings.Fields(string(u))
		if len(fields) == 0 {
			return "", 0, false
		}
		secs, err := strconv.ParseFloat(fields[0], 64)
		if err != nil || secs < 0 {
			return "", 0, false
		}
		return id, time.Duration(secs * float64(time.Second)), true
	}
}

// ClockSynced tells whether the clock follows network time: the kernel's own
// flag (adjtimex) where the sandbox allows it, else systemd-timesyncd's flag
// file. nasbusterd runs with ProtectClock=yes, which forbids even a read-only
// adjtimex (seen on the Pi 5: "unknown"); timesyncd, Raspberry Pi OS's NTP
// client, creates run/systemd/timesync/synchronized once synchronized.
func ClockSynced(root fs.FS) func() (synced, known bool) {
	return func() (bool, bool) {
		if synced, known := KernelClockSynced(); known {
			return synced, true
		}
		if _, err := fs.Stat(root, "run/systemd/timesync/synchronized"); err == nil {
			return true, true
		} else if !errors.Is(err, fs.ErrNotExist) {
			return false, false
		}
		// timesyncd runs (its directory exists) but has not synchronized yet.
		if _, err := fs.Stat(root, "run/systemd/timesync"); err == nil {
			return false, true
		}
		return false, false
	}
}
