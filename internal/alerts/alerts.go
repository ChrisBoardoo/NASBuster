// SPDX-License-Identifier: AGPL-3.0-or-later

// Package alerts turns the NAS state into alerts and notifications (ADR-0011).
//
// An alert is a STATE identified by a stable id ("pool.degraded:tank"). It is
// notified when it appears and when it is resolved — never repeatedly while
// it lasts.
package alerts

import (
	"fmt"
	"sort"
	"strings"

	"github.com/crosswax/nasbuster/internal/storage"
	"github.com/crosswax/nasbuster/internal/system"
)

// Levels.
const (
	Warn = "warn"
	Crit = "crit"
)

// Condition is an alert that currently holds.
type Condition struct {
	ID     string         `json:"id"`
	Level  string         `json:"level"`
	Code   string         `json:"code"`   // i18n key suffix: alerts.<code>
	Params map[string]any `json:"params"` // raw values for the message
}

// PoolInput is a pool's name and live status (nil when unknown).
type PoolInput struct {
	Name   string
	Status *storage.PoolStatus
}

// SmartInput is the last SMART read of a pool member, and the previous one
// (for counters that matter when they grow, like UDMA CRC errors).
type SmartInput struct {
	DiskID   string
	Current  storage.SmartReport
	Previous *storage.SmartReport
}

// Evaluate returns every condition that holds now, sorted by id.
func Evaluate(pools []PoolInput, checks []system.Check, smart []SmartInput) []Condition {
	var out []Condition
	add := func(id, level, code string, params map[string]any) {
		if params == nil {
			params = map[string]any{}
		}
		out = append(out, Condition{ID: id, Level: level, Code: code, Params: params})
	}

	for _, p := range pools {
		if p.Status == nil {
			continue // helper unreachable: not a pool problem in itself
		}
		params := map[string]any{"pool": p.Name}
		switch p.Status.Health {
		case storage.HealthUnmounted:
			if p.Status.CanReassemble {
				// After a reboot: every disk back, the array waits (ADR-0032).
				add("pool.reassemble:"+p.Name, Crit, "pool.reassemble", params)
			} else if p.Status.CanMountDegraded {
				add("pool.missing_disk:"+p.Name, Crit, "pool.missing_disk", params)
			} else if r := p.Status.Reassemble; r != nil && r.Reason == storage.ReassembleMissingDisks {
				// Not started, and more disks gone than the parity tolerates.
				add("pool.failed:"+p.Name, Crit, "pool.failed", map[string]any{"pool": p.Name, "missing": r.Expected - len(r.Members)})
			} else if r != nil && r.Reason != "" {
				// Every disk there, but they disagree (two on one role, one far
				// behind): never forced, a manual recovery (ADR-0032).
				add("pool.manual_recovery:"+p.Name, Crit, "pool.manual_recovery", map[string]any{"pool": p.Name, "reason": r.Reason})
			} else {
				add("pool.unmounted:"+p.Name, Crit, "pool.unmounted", params)
			}
		case storage.HealthDegraded:
			// "Degraded" covers three states, seen on the Pi 5: only the first
			// means that a disk is missing (or failing).
			st, md := p.Status, p.Status.MD
			switch {
			case st.CanReassemble:
				// Broken, every disk back: one click restarts it (ADR-0032).
				add("pool.reassemble:"+p.Name, Crit, "pool.reassemble", params)
			case md != nil && md.Broken():
				// More disks gone than the parity tolerates: no data at all.
				add("pool.failed:"+p.Name, Crit, "pool.failed", map[string]any{"pool": p.Name, "missing": md.Expected - md.Active})
			case st.MissingDevices > 0 || st.Missing > 0 || (md != nil && md.Degraded() && !md.Rebuilding()):
				add("pool.degraded:"+p.Name, Crit, "pool.degraded", params)
			case (md != nil && md.Rebuilding()) || st.MixedProfiles:
				// Every disk there: md rebuilds one (a new array, a disk taken
				// back or added), or Btrfs converts the chunks written while degraded.
				add("pool.rebuilding:"+p.Name, Warn, "pool.rebuilding", params)
			default:
				// Only the scrub owed after a period without redundancy (ADR-0027).
				add("pool.verifying:"+p.Name, Warn, "pool.verifying", params)
			}
		case storage.HealthErrors:
			add("pool.errors:"+p.Name, Crit, "pool.errors", params)
		}
	}

	for _, c := range checks {
		switch c.Code {
		case "power.under_voltage_now":
			add("power.under_voltage", Crit, "power.under_voltage_now", nil)
		case "power.under_voltage_occurred":
			add("power.under_voltage", Warn, "power.under_voltage_occurred", nil)
		case "temperature.hot":
			add("temperature.hot", Crit, "temperature.hot", c.Params)
		case "throttling.active":
			add("throttling.active", Warn, "throttling.active", nil)
		}
	}

	for _, s := range smart {
		if !s.Current.Available {
			continue
		}
		params := map[string]any{"disk": s.DiskID}
		if s.Current.Passed != nil && !*s.Current.Passed {
			add("smart.failing:"+s.DiskID, Crit, "smart.failing", params)
		}
		var bad []int
		for _, a := range s.Current.Attributes {
			if a.ID != 199 && a.Raw > 0 {
				bad = append(bad, a.ID)
			}
		}
		if len(bad) > 0 {
			p := map[string]any{"disk": s.DiskID, "attributes": bad}
			add("smart.attributes:"+s.DiskID, Warn, "smart.attributes", p)
		}
		if crc, prev := attr(s.Current, 199), previousAttr(s.Previous, 199); crc > prev && s.Previous != nil {
			p := map[string]any{"disk": s.DiskID, "count": crc, "increase": crc - prev}
			add("smart.crc:"+s.DiskID, Warn, "smart.crc", p)
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func attr(r storage.SmartReport, id int) int64 {
	for _, a := range r.Attributes {
		if a.ID == id {
			return a.Raw
		}
	}
	return 0
}

func previousAttr(r *storage.SmartReport, id int) int64 {
	if r == nil {
		return 0
	}
	return attr(*r, id)
}

// Transition is what changed between two evaluations.
type Transition struct {
	Raised   []Condition // new, or whose level got worse
	Resolved []string    // ids that no longer hold
}

// Diff compares the active alerts (id -> level) with what holds now.
func Diff(active map[string]string, now []Condition) Transition {
	var t Transition
	seen := map[string]bool{}
	for _, c := range now {
		seen[c.ID] = true
		prev, was := active[c.ID]
		if !was || (prev == Warn && c.Level == Crit) {
			t.Raised = append(t.Raised, c)
		}
	}
	for id := range active {
		if !seen[id] {
			t.Resolved = append(t.Resolved, id)
		}
	}
	sort.Strings(t.Resolved)
	return t
}

// Title renders a short notification title in the admin's language.
func Title(code, lang string, params map[string]any) string {
	cat := catalog["en"]
	if c, ok := catalog[lang]; ok {
		cat = c
	}
	tmpl, ok := cat[code]
	if !ok {
		return code
	}
	// Sizes are kept in bytes; a notification reads them in the admin's units.
	if n, ok := asBytes(params["freeBytes"]); ok {
		with := make(map[string]any, len(params)+1)
		for k, v := range params {
			with[k] = v
		}
		with["free"] = formatBytes(n, lang)
		params = with
	}
	return expand(tmpl, params)
}

// asBytes reads a byte count from params, as Go or from JSON (float64).
func asBytes(v any) (uint64, bool) {
	switch n := v.(type) {
	case uint64:
		return n, true
	case int64:
		return uint64(max(n, 0)), true
	case int:
		return uint64(max(n, 0)), true
	case float64:
		return uint64(max(n, 0)), true
	}
	return 0, false
}

// ResolvedTitle renders the "back to normal" title.
func ResolvedTitle(code, lang string, params map[string]any) string {
	prefix := "Resolved: "
	if lang == "fr" {
		prefix = "Résolu : "
	}
	return prefix + Title(code, lang, params)
}

func expand(tmpl string, params map[string]any) string {
	out := []rune{}
	r := []rune(tmpl)
	for i := 0; i < len(r); i++ {
		if r[i] == '{' {
			end := i + 1
			for end < len(r) && r[end] != '}' {
				end++
			}
			if end < len(r) {
				out = append(out, []rune(fmt.Sprint(params[string(r[i+1:end])]))...)
				i = end
				continue
			}
		}
		out = append(out, r[i])
	}
	return string(out)
}

// catalog holds notification titles. No secret, no file path; {disk} is
// rendered from Public params, so never a full serial.
var catalog = map[string]map[string]string{
	"en": {
		"pool.unmounted":               "Pool {pool} is not mounted",
		"pool.missing_disk":            "Pool {pool} cannot start: a disk is missing",
		"pool.degraded":                "Pool {pool} is degraded: a disk is missing",
		"pool.rebuilding":              "Pool {pool} is rebuilding its redundancy: no safety net until it ends",
		"pool.verifying":               "Pool {pool} is checking its data after a period without redundancy",
		"pool.reassemble":              "Pool {pool}: every disk is back, it waits to be reassembled (Storage page)",
		"pool.failed":                  "Pool {pool} is out of service: {missing} disks are gone, more than it tolerates. Touch nothing, plug them back",
		"pool.manual_recovery":         "Pool {pool} is out of service and needs a manual recovery. Touch nothing on its disks",
		"pool.errors":                  "Pool {pool} reports read or checksum errors",
		"power.under_voltage_now":      "Under-voltage right now: check the power supply",
		"power.under_voltage_occurred": "Under-voltage happened since boot: check the power supply",
		"temperature.hot":              "The NAS is too hot ({celsius} °C)",
		"throttling.active":            "The CPU is being throttled",
		"smart.failing":                "Disk {disk} fails its SMART self-assessment: replace it",
		"smart.attributes":             "Disk {disk} shows bad sectors (SMART {attributes})",
		"smart.crc":                    "Disk {disk}: {increase} new transfer errors (cable or USB bridge)",
		"test":                         "Test notification from CrossWax NasBuster",
		"auth.password_reset":          "The NAS admin password was reset from {ip}. If it was not you, change it and check the machine account",
		"pool.space":                   "Pool {pool} is over {threshold} % full: {free} left",
		"system.space":                 "The NAS system disk (SD card) is over {threshold} % full: {free} left",
	},
	"fr": {
		"pool.unmounted":               "Le pool {pool} n'est pas monté",
		"pool.missing_disk":            "Le pool {pool} ne peut pas démarrer : un disque manque",
		"pool.degraded":                "Le pool {pool} est dégradé : un disque manque",
		"pool.rebuilding":              "Le pool {pool} reconstruit sa redondance : pas de filet de sécurité avant la fin",
		"pool.verifying":               "Le pool {pool} vérifie ses données après une période sans redondance",
		"pool.reassemble":              "Pool {pool} : tous ses disques sont revenus, il attend d'être réassemblé (page Stockage)",
		"pool.failed":                  "Le pool {pool} est hors service : {missing} disques ont disparu, plus qu'il n'en tolère. Ne touche à rien, rebranche-les",
		"pool.manual_recovery":         "Le pool {pool} est hors service et demande une récupération manuelle. Ne touche à rien sur ses disques",
		"pool.errors":                  "Le pool {pool} signale des erreurs de lecture ou de checksum",
		"power.under_voltage_now":      "Sous-tension en ce moment : vérifie l'alimentation",
		"power.under_voltage_occurred": "Une sous-tension est survenue depuis le démarrage : vérifie l'alimentation",
		"temperature.hot":              "Le NAS est trop chaud ({celsius} °C)",
		"throttling.active":            "Le processeur est bridé",
		"smart.failing":                "Le disque {disk} échoue à son autodiagnostic SMART : remplace-le",
		"smart.attributes":             "Le disque {disk} présente des secteurs défectueux (SMART {attributes})",
		"smart.crc":                    "Disque {disk} : {increase} nouvelles erreurs de transfert (câble ou bridge USB)",
		"test":                         "Notification de test de CrossWax NasBuster",
		"auth.password_reset":          "Le mot de passe admin du NAS a été réinitialisé depuis {ip}. Si ce n'est pas toi, change-le et vérifie le compte de la machine",
		"pool.space":                   "Le pool {pool} est rempli à plus de {threshold} % : il reste {free}",
		"system.space":                 "Le disque système du NAS (carte SD) est rempli à plus de {threshold} % : il reste {free}",
	},
}

// DiskLabel turns a /dev/disk/by-id name into a label that is enough to find
// the disk without publishing its full serial (ntfy topics are public by name):
// "ata-Samsung_SSD_870_EVO_1TB_S6PUNX0R123456A" -> "Samsung SSD 870 EVO 1TB (…456A)".
func DiskLabel(id string) string {
	tail := func(s string) string {
		r := []rune(s)
		if len(r) > 4 {
			r = r[len(r)-4:]
		}
		return string(r)
	}
	bus, rest, ok := strings.Cut(id, "-")
	if !ok || rest == "" {
		return "disk …" + tail(id)
	}
	switch bus {
	case "ata", "usb", "nvme", "scsi", "mmc":
	default: // wwn-, eui-…: an opaque unique number
		return "disk …" + tail(rest)
	}
	if i := strings.LastIndex(rest, "-"); i > 0 && strings.Contains(rest[i:], ":") {
		rest = rest[:i] // USB LUN suffix "-0:0"
	}
	i := strings.LastIndex(rest, "_")
	if i <= 0 || i == len(rest)-1 {
		return "disk …" + tail(rest)
	}
	model := strings.ReplaceAll(rest[:i], "_", " ")
	return model + " (…" + tail(rest[i+1:]) + ")"
}

// Public returns the params as they may leave the NAS: disk ids become labels.
func Public(params map[string]any) map[string]any {
	out := make(map[string]any, len(params))
	for k, v := range params {
		if s, ok := v.(string); ok && k == "disk" {
			v = DiskLabel(s)
		}
		out[k] = v
	}
	return out
}
