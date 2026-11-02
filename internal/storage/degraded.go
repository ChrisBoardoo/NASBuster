// SPDX-License-Identifier: AGPL-3.0-or-later

package storage

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Failure handling (ADR-0012): parsers for the outputs that describe a missing
// disk, a running replace or balance, and redundancy lost while degraded.

// ShowDevice is one device line of `btrfs filesystem show --raw`.
type ShowDevice struct {
	DevID     int
	SizeBytes uint64
	Path      string
	Missing   bool
}

// FilesystemShow is `btrfs filesystem show --raw <mnt|uuid>`.
type FilesystemShow struct {
	Total       int // devices the filesystem expects
	Devices     []ShowDevice
	SomeMissing bool // "*** Some devices missing" (unmounted filesystems)
}

// MissingCount is how many expected devices are absent. Mounted degraded,
// btrfs lists the missing devid ("path MISSING"); unmounted, it only lists
// the present ones and prints "*** Some devices missing".
func (f FilesystemShow) MissingCount() int {
	present := 0
	for _, d := range f.Devices {
		if !d.Missing {
			present++
		}
	}
	if n := f.Total - present; n > 0 {
		return n
	}
	return 0
}

// DevIDOf returns the devid whose path has this kernel name (sda1, loop2p1).
func (f FilesystemShow) DevIDOf(kernel string) (int, bool) {
	for _, d := range f.Devices {
		if !d.Missing && strings.TrimPrefix(d.Path, "/dev/") == kernel {
			return d.DevID, true
		}
	}
	return 0, false
}

// MissingDevIDs lists the devids reported missing (mounted filesystems only).
func (f FilesystemShow) MissingDevIDs() []int {
	var ids []int
	for _, d := range f.Devices {
		if d.Missing {
			ids = append(ids, d.DevID)
		}
	}
	return ids
}

var (
	showTotalRe  = regexp.MustCompile(`Total devices (\d+)`)
	showDeviceRe = regexp.MustCompile(`^\s*devid\s+(\d+)\s+size\s+(\d+)\s+used\s+\d+\s+path\s*(.*)$`)
)

// ParseFilesystemShow parses `btrfs filesystem show --raw` (no JSON in btrfs-progs 6.14).
func ParseFilesystemShow(text string) FilesystemShow {
	var f FilesystemShow
	for _, line := range strings.Split(text, "\n") {
		if m := showTotalRe.FindStringSubmatch(line); m != nil {
			f.Total, _ = strconv.Atoi(m[1])
			continue
		}
		if strings.Contains(line, "Some devices missing") {
			f.SomeMissing = true
			continue
		}
		if m := showDeviceRe.FindStringSubmatch(line); m != nil {
			id, _ := strconv.Atoi(m[1])
			size, _ := strconv.ParseUint(m[2], 10, 64)
			p := strings.TrimSpace(m[3])
			f.Devices = append(f.Devices, ShowDevice{DevID: id, SizeBytes: size, Path: p,
				Missing: p == "" || strings.Contains(strings.ToLower(p), "missing")})
		}
	}
	return f
}

// ReplaceStatus is a `btrfs replace` in progress or just finished.
type ReplaceStatus struct {
	State       string  `json:"state"` // running, finished, canceled
	Percent     float64 `json:"percent"`
	WriteErrors int64   `json:"writeErrors"`
	ReadErrors  int64   `json:"readErrors"` // uncorrectable read errors
	FinishedAt  int64   `json:"finishedAt"` // unix seconds, 0 if unknown
}

var (
	replaceRunningRe  = regexp.MustCompile(`([\d.]+)% done`)
	replaceErrorsRe   = regexp.MustCompile(`(\d+) write errs, (\d+) uncorr\. read errs`)
	replaceFinishedRe = regexp.MustCompile(`finished on\s+(\d{1,2}\.[A-Z][a-z]{2} \d{2}:\d{2}:\d{2})`)
)

// replaceNow is the clock that gives btrfs' year-less dates their year.
var replaceNow = time.Now

// replaceTime reads btrfs' "%e.%b %T" (" 4.Oct 11:30:00", local time, no
// year): this year, or last year if that would be in the future.
func replaceTime(v string) int64 {
	t, err := time.ParseInLocation("2.Jan 15:04:05", strings.TrimSpace(v), time.Local)
	if err != nil {
		return 0
	}
	now := replaceNow()
	t = t.AddDate(now.Year()-t.Year(), 0, 0)
	if t.After(now.Add(24 * time.Hour)) {
		t = t.AddDate(-1, 0, 0)
	}
	return t.Unix()
}

// ParseReplaceStatus parses `btrfs replace status -1 <mnt>`; nil if never started.
func ParseReplaceStatus(text string) *ReplaceStatus {
	text = strings.TrimSpace(text)
	var r ReplaceStatus
	switch {
	case text == "" || strings.Contains(text, "Never started"):
		return nil
	case strings.Contains(text, "finished on"):
		r.State, r.Percent = "finished", 100
		if m := replaceFinishedRe.FindStringSubmatch(text); m != nil {
			r.FinishedAt = replaceTime(m[1])
		}
	case strings.Contains(text, "canceled"):
		r.State = "canceled"
	case replaceRunningRe.MatchString(text):
		r.State = "running"
		r.Percent, _ = strconv.ParseFloat(replaceRunningRe.FindStringSubmatch(text)[1], 64)
	default:
		return nil
	}
	if m := replaceErrorsRe.FindStringSubmatch(text); m != nil {
		r.WriteErrors, _ = strconv.ParseInt(m[1], 10, 64)
		r.ReadErrors, _ = strconv.ParseInt(m[2], 10, 64)
	}
	return &r
}

// BalanceStatus is a `btrfs balance` running or paused.
type BalanceStatus struct {
	State       string `json:"state"` // running, paused
	Done        int    `json:"done"`
	Total       int    `json:"total"`
	PercentLeft int    `json:"percentLeft"`
}

var balanceProgressRe = regexp.MustCompile(`(\d+) out of about (\d+) chunks balanced.*?(\d+)% left`)

// ParseBalanceStatus parses `btrfs balance status <mnt>` (exit 1 while one
// runs or is paused, 0 otherwise); nil when no balance exists.
func ParseBalanceStatus(text string) *BalanceStatus {
	var b BalanceStatus
	switch {
	case strings.Contains(text, " is running"):
		b.State = "running"
	case strings.Contains(text, " is paused"):
		b.State = "paused"
	default:
		return nil
	}
	if m := balanceProgressRe.FindStringSubmatch(text); m != nil {
		b.Done, _ = strconv.Atoi(m[1])
		b.Total, _ = strconv.Atoi(m[2])
		b.PercentLeft, _ = strconv.Atoi(m[3])
	}
	return &b
}

// MixedProfiles reports chunks of one type stored with different profiles:
// what writes made while mounted degraded leave behind ("single" next to
// "RAID1"). Those chunks have no redundancy until a convert balance.
func MixedProfiles(profiles []ProfileUsage) bool {
	seen := map[string]string{}
	for _, p := range profiles {
		if p.Type != "Data" && p.Type != "Metadata" && p.Type != "System" {
			continue
		}
		if prev, ok := seen[p.Type]; ok && !strings.EqualFold(prev, p.Profile) {
			return true
		}
		seen[p.Type] = p.Profile
	}
	return false
}

// TargetProfiles returns the redundant profile already used for data and
// metadata (lower case, as `btrfs balance -dconvert=` expects), or "" for a
// type that only has "single" chunks (nothing to restore).
func TargetProfiles(profiles []ProfileUsage) (data, metadata string) {
	best := map[string]ProfileUsage{}
	for _, p := range profiles {
		if (p.Type != "Data" && p.Type != "Metadata") || strings.EqualFold(p.Profile, "single") {
			continue
		}
		if cur, ok := best[p.Type]; !ok || p.Total > cur.Total {
			best[p.Type] = p
		}
	}
	return strings.ToLower(best["Data"].Profile), strings.ToLower(best["Metadata"].Profile)
}
