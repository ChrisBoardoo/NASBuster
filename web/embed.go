// SPDX-License-Identifier: AGPL-3.0-or-later

// Package web embeds the built UI (web/dist) into the nasbusterd binary.
//
// dist/.keep is committed so the embed pattern always matches, even before
// the first `pnpm build`. The build script recreates it after Vite empties dist/.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Dist returns the built UI rooted at dist/.
func Dist() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // "dist" is a compile-time constant: cannot fail
	}
	return sub
}
