// SPDX-License-Identifier: AGPL-3.0-or-later

// Package fixture loads small text archives into an in-memory fs.FS.
//
// The format is the txtar convention: each file starts with a line
// "-- path/inside/fs --" and its content runs until the next marker.
// One archive can describe a whole fake system tree (/proc, /sys, /etc),
// including paths such as "soc:firmware" that cannot exist on Windows disks.
// A marker of the form "-- path -> target --" declares a symlink.
package fixture

import (
	"io/fs"
	"strings"
	"testing/fstest"
)

// Parse turns an archive into a MapFS. Text before the first marker is ignored.
func Parse(data []byte) fstest.MapFS {
	fsys := fstest.MapFS{}
	for _, s := range Sections(data) {
		if target, ok := symlinkTarget(s.Name); ok {
			link := strings.TrimSpace(s.Name[:strings.Index(s.Name, " -> ")])
			fsys[link] = &fstest.MapFile{Data: []byte(target), Mode: fs.ModeSymlink | 0o777}
		} else {
			fsys[s.Name] = &fstest.MapFile{Data: s.Data, Mode: 0o644}
		}
	}
	return fsys
}

// Section is one "-- name --" block of an archive.
type Section struct {
	Name string
	Data []byte
}

// Sections splits an archive into its blocks, in order. Names are free-form
// (they may contain spaces or start with "/"), which suits command outputs.
func Sections(data []byte) []Section {
	var out []Section
	var name string
	var content strings.Builder
	flush := func() {
		if name != "" {
			out = append(out, Section{Name: name, Data: []byte(content.String())})
		}
		content.Reset()
	}
	for _, line := range strings.SplitAfter(string(data), "\n") {
		trimmed := strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(trimmed, "-- ") && strings.HasSuffix(trimmed, " --") && len(trimmed) > 6 {
			flush()
			name = strings.TrimSpace(trimmed[3 : len(trimmed)-3])
			continue
		}
		if name != "" {
			content.WriteString(line)
		}
	}
	flush()
	return out
}

func symlinkTarget(name string) (string, bool) {
	i := strings.Index(name, " -> ")
	if i < 0 {
		return "", false
	}
	return strings.TrimSpace(name[i+4:]), true
}
