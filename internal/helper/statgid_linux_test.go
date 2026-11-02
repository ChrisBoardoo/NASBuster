// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build integration && linux

package helper

import (
	"io/fs"
	"syscall"
)

func statGID(info fs.FileInfo) uint32 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return st.Gid
	}
	return 0
}
