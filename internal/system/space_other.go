// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !linux

package system

// FilesystemSpace is unknown outside Linux (development machines).
func FilesystemSpace(string) (used, size uint64, ok bool) { return 0, 0, false }
