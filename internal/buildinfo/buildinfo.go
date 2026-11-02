// SPDX-License-Identifier: AGPL-3.0-or-later

// Package buildinfo holds values injected at link time.
package buildinfo

// Version is overridden at build time with
// -ldflags "-X github.com/crosswax/nasbuster/internal/buildinfo.Version=<version>".
var Version = "0.1.0-dev"
