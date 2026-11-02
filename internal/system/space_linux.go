// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package system

import "syscall"

// FilesystemSpace reads how full the filesystem holding path is, as df shows
// it: used blocks, and the size a user can fill (used plus what is still
// available to non-root). A read-only statfs call, no privilege, no command.
func FilesystemSpace(path string) (used, size uint64, ok bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil || st.Blocks == 0 {
		return 0, 0, false
	}
	bsize := uint64(st.Bsize)
	used = (st.Blocks - st.Bfree) * bsize
	return used, used + st.Bavail*bsize, true
}
