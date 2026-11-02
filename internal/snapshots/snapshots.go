// SPDX-License-Identifier: AGPL-3.0-or-later

// Package snapshots names share snapshots the way Samba's shadow_copy2
// expects ("@GMT-2026.10.01-21.00.00", UTC) and decides which ones a
// retention policy keeps (ADR-0010).
package snapshots

import (
	"fmt"
	"regexp"
	"sort"
	"time"
)

// Layout is the time layout of snapshot names (shadow:format, UTC).
const Layout = "@GMT-2006.01.02-15.04.05"

// Dir is the snapshot directory inside each share.
const Dir = ".snapshots"

var nameRe = regexp.MustCompile(`^@GMT-\d{4}\.\d{2}\.\d{2}-\d{2}\.\d{2}\.\d{2}$`)

// Name returns the snapshot name for t.
func Name(t time.Time) string { return t.UTC().Format(Layout) }

// Parse returns the time encoded in a snapshot name.
func Parse(name string) (time.Time, bool) {
	if !nameRe.MatchString(name) {
		return time.Time{}, false
	}
	t, err := time.Parse(Layout, name)
	return t, err == nil
}

// ValidName reports whether name is a well-formed snapshot name. It is the
// only kind of name the helper will ever delete.
func ValidName(name string) bool {
	_, ok := Parse(name)
	return ok
}

// Policy is how many snapshots to keep per period (grandfather-father-son).
type Policy struct {
	Hourly  int `json:"hourly"`
	Daily   int `json:"daily"`
	Weekly  int `json:"weekly"`
	Monthly int `json:"monthly"`
}

// DefaultPolicy keeps a day of hours, a week of days, a month of weeks and
// half a year of months.
var DefaultPolicy = Policy{Hourly: 24, Daily: 7, Weekly: 4, Monthly: 6}

// Valid bounds the policy (a typo must not keep thousands of snapshots).
func (p Policy) Valid() bool {
	in := func(v, max int) bool { return v >= 0 && v <= max }
	return in(p.Hourly, 168) && in(p.Daily, 90) && in(p.Weekly, 104) && in(p.Monthly, 120) &&
		p.Hourly+p.Daily+p.Weekly+p.Monthly > 0
}

// Prune returns the snapshot names to delete. In each of the most recent
// Hourly hours (Daily days, Weekly ISO weeks, Monthly months) that holds a
// snapshot, the newest one is kept; everything else goes. Names that do not
// parse are never returned (never touch what we do not understand).
func Prune(names []string, p Policy) []string {
	type snap struct {
		name string
		t    time.Time
	}
	var snaps []snap
	for _, n := range names {
		if t, ok := Parse(n); ok {
			snaps = append(snaps, snap{n, t})
		}
	}
	sort.Slice(snaps, func(i, j int) bool { return snaps[i].t.After(snaps[j].t) }) // newest first

	keep := map[string]bool{}
	bucket := func(count int, key func(time.Time) string) {
		seen := map[string]bool{}
		for _, s := range snaps {
			if len(seen) >= count {
				return
			}
			k := key(s.t)
			if !seen[k] {
				seen[k] = true
				keep[s.name] = true // newest in its bucket
			}
		}
	}
	bucket(p.Hourly, func(t time.Time) string { return t.Format("2006010215") })
	bucket(p.Daily, func(t time.Time) string { return t.Format("20060102") })
	bucket(p.Weekly, func(t time.Time) string {
		y, w := t.ISOWeek()
		return fmt.Sprintf("%d-W%02d", y, w)
	})
	bucket(p.Monthly, func(t time.Time) string { return t.Format("200601") })

	var del []string
	for _, s := range snaps {
		if !keep[s.name] {
			del = append(del, s.name)
		}
	}
	return del
}
