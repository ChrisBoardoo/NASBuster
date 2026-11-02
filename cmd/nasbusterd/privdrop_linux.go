// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package main

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"strconv"
	"syscall"
)

// becomeServiceUser drops from root to the daemon's account, so that the
// files it touches in the state directory keep their owner. Any other
// account is refused: only root (sudo) proves ownership of the machine.
func becomeServiceUser() error {
	if os.Geteuid() != 0 {
		if u, err := user.Current(); err == nil && u.Username == serviceUser {
			return nil
		}
		return errors.New("run it with sudo: sudo nasbusterd -reset-admin")
	}
	u, err := user.Lookup(serviceUser)
	if err != nil {
		return fmt.Errorf("account %s not found: is NasBuster installed? %w", serviceUser, err)
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return err
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return err
	}
	// Groups first, then the group, then the user: once the user changes,
	// the rest is no longer allowed. Go applies these to every thread.
	if err := syscall.Setgroups([]int{}); err != nil {
		return fmt.Errorf("drop supplementary groups: %w", err)
	}
	if err := syscall.Setgid(gid); err != nil {
		return fmt.Errorf("switch to group %s: %w", serviceUser, err)
	}
	if err := syscall.Setuid(uid); err != nil {
		return fmt.Errorf("switch to user %s: %w", serviceUser, err)
	}
	return nil
}
