// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package helper

import (
	"errors"
	"net"

	"golang.org/x/sys/unix"
)

// peerUID returns the uid of the process at the other end of a Unix socket,
// as certified by the kernel (SO_PEERCRED): it cannot be forged by the caller.
func peerUID(conn net.Conn) (uint32, error) {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return 0, errors.New("not a unix socket")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return 0, err
	}
	var cred *unix.Ucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return 0, err
	}
	if credErr != nil {
		return 0, credErr
	}
	return cred.Uid, nil
}
