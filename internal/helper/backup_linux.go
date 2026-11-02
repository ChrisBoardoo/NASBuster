// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package helper

import (
	"fmt"
	"io"
	"os"
	"syscall"

	"github.com/crosswax/nasbuster/internal/backup"
)

// readDaemonExport reads backup.DaemonExport safely.
func readDaemonExport() ([]byte, error) {
	f, err := os.OpenFile(backup.DaemonExport, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > backup.MaxDatabase {
		return nil, fmt.Errorf("%s is not a regular file of at most %d bytes", backup.DaemonExport, backup.MaxDatabase)
	}
	return io.ReadAll(io.LimitReader(f, backup.MaxDatabase+1))
}
