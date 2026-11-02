// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !linux

package helper

import (
	"errors"
	"net"
)

// peerUID is unavailable off Linux: the helper refuses every caller there.
func peerUID(net.Conn) (uint32, error) {
	return 0, errors.New("peer credentials are only supported on Linux")
}
