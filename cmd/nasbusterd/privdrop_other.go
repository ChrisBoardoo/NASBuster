// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !linux

package main

// becomeServiceUser has nothing to drop outside Linux (the demo on the dev PC).
func becomeServiceUser() error { return nil }
