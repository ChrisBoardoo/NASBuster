// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package system

import "syscall"

// staUnsync is the kernel's "clock not synchronized" status bit (linux/timex.h).
const staUnsync = 0x0040

// KernelClockSynced reads the kernel's own flag, like timedatectl's
// NTPSynchronized: a read-only adjtimex call, no privilege, no command.
// systemd-timesyncd (or chrony) clears it once synchronized.
func KernelClockSynced() (synced, known bool) {
	var tx syscall.Timex
	if _, err := syscall.Adjtimex(&tx); err != nil {
		return false, false
	}
	return tx.Status&staUnsync == 0, true
}
