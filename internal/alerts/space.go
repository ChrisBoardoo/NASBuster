// SPDX-License-Identifier: AGPL-3.0-or-later

package alerts

import (
	"fmt"
	"strings"
)

// Space alerts (ADR-0034, A4): a full pool blocks SMB writes, Docker and the
// snapshots; a full SD card can keep the system from starting. Seen on the
// Pi 5: 404 MB of an application's error log written in a few hours, with
// nothing to warn.
const (
	SpaceWarnPercent = 85 // OMV's monit default, as far as we know (unchecked in its sources)
	SpaceCritPercent = 95
	// spaceHysteresis: an alert falls back only this many points below its
	// threshold, so that a pool hovering at 85 % does not notify every five
	// minutes.
	spaceHysteresis = 2
)

// SpaceInput is how full a filesystem is: a pool, or the system disk (the SD
// card) when Pool is "". Used and Size are what the user sees: for a pool,
// the logical used space and the usable capacity (RAID copies counted once).
type SpaceInput struct {
	Pool      string
	UsedBytes uint64
	SizeBytes uint64
}

// EvaluateSpace returns the space alerts that hold. active gives the level of
// the alerts active now (id -> level), for the hysteresis.
func EvaluateSpace(space []SpaceInput, active map[string]string) []Condition {
	var out []Condition
	for _, s := range space {
		if s.SizeBytes == 0 {
			continue
		}
		used := min(s.UsedBytes, s.SizeBytes)
		percent := int(used * 100 / s.SizeBytes)
		id, code := "system.space", "system.space"
		params := map[string]any{"percent": percent, "freeBytes": s.SizeBytes - used}
		if s.Pool != "" {
			id, code = "pool.space:"+s.Pool, "pool.space"
			params["pool"] = s.Pool
		}
		was := active[id]
		level := ""
		switch {
		case percent >= SpaceCritPercent || (was == Crit && percent >= SpaceCritPercent-spaceHysteresis):
			level, params["threshold"] = Crit, SpaceCritPercent
		case percent >= SpaceWarnPercent || (was != "" && percent >= SpaceWarnPercent-spaceHysteresis):
			level, params["threshold"] = Warn, SpaceWarnPercent
		}
		if level != "" {
			out = append(out, Condition{ID: id, Level: level, Code: code, Params: params})
		}
	}
	return out
}

// formatBytes renders a size for a notification, in SI units as the UI does.
func formatBytes(n uint64, lang string) string {
	units := []string{"B", "kB", "MB", "GB", "TB", "PB"}
	if lang == "fr" {
		units = []string{"o", "ko", "Mo", "Go", "To", "Po"}
	}
	v, i := float64(n), 0
	for v >= 1000 && i < len(units)-1 {
		v /= 1000
		i++
	}
	s := fmt.Sprintf("%.1f", v)
	if i == 0 || v >= 100 {
		s = fmt.Sprintf("%.0f", v)
	}
	if lang == "fr" {
		s = strings.Replace(s, ".", ",", 1)
	}
	return s + " " + units[i]
}
