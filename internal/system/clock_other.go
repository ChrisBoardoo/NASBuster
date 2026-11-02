// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !linux

package system

// KernelClockSynced is unknown outside Linux (development machines).
func KernelClockSynced() (synced, known bool) { return false, false }
