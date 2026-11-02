// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !linux

package helper

import "errors"

// readDaemonExport only exists on Linux (O_NOFOLLOW); elsewhere the helper
// runs in demo or test mode with Ops.ReadDaemonExport set.
func readDaemonExport() ([]byte, error) {
	return nil, errors.New("configuration backups need Linux")
}
