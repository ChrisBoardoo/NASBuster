// SPDX-License-Identifier: AGPL-3.0-or-later

package storage

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Pool health values.
const (
	HealthOK        = "ok"
	HealthDegraded  = "degraded"
	HealthErrors    = "errors"
	HealthUnmounted = "unmounted"
	HealthUnknown   = "unknown"
)

// DeviceErrors are the Btrfs per-device error counters.
type DeviceErrors struct {
	Write      int64 `json:"write"`
	Read       int64 `json:"read"`
	Flush      int64 `json:"flush"`
	Corruption int64 `json:"corruption"`
	Generation int64 `json:"generation"`
}

// Total sums all counters.
func (e DeviceErrors) Total() int64 { return e.Write + e.Read + e.Flush + e.Corruption + e.Generation }

// PoolDevice is one Btrfs member.
type PoolDevice struct {
	Path      string       `json:"path"` // "devid:2" when missing
	DevID     int          `json:"devId"`
	SizeBytes uint64       `json:"sizeBytes"`
	Missing   bool         `json:"missing"`
	Errors    DeviceErrors `json:"errors"`
}

// ProfileUsage is one line of `btrfs filesystem df`.
type ProfileUsage struct {
	Type    string `json:"type"`
	Profile string `json:"profile"`
	Total   uint64 `json:"totalBytes"`
	Used    uint64 `json:"usedBytes"`
}

// ScrubStatus summarises `btrfs scrub status -R`.
type ScrubStatus struct {
	Status          string `json:"status"`          // never, running, finished, aborted, interrupted
	Started         string `json:"started"`         // raw btrfs text, server local time (compatibility)
	Duration        string `json:"duration"`        // raw "H:MM:SS"
	StartedAt       int64  `json:"startedAt"`       // unix seconds, 0 if unknown
	DurationSeconds int64  `json:"durationSeconds"` // 0 if unknown
	Corrected       int64  `json:"corrected"`
	Uncorrectable   int64  `json:"uncorrectable"`
	CsumErrors      int64  `json:"csumErrors"`
	ReadErrors      int64  `json:"readErrors"`
	// Resumed: the last run went on with a stopped scrub ("Scrub resumed");
	// Started is then the time of the resume, not of the first pass.
	Resumed bool `json:"resumed"`
}

// MDSync is a running resync/recovery/check.
type MDSync struct {
	Action  string  `json:"action"` // resync, recovery, check, reshape
	Percent float64 `json:"percent"`
}

// MDStatus describes the md array under a parity pool.
type MDStatus struct {
	Device   string   `json:"device"`
	State    string   `json:"state"` // "active", "inactive" (members held, not started), "broken"
	Level    string   `json:"level"`
	Active   int      `json:"active"`
	Expected int      `json:"expected"`
	Members  []string `json:"members"` // kernel names, faulty ones included
	Faulty   []string `json:"faulty"`  // marked (F) in /proc/mdstat
	Sync     *MDSync  `json:"sync"`
	// Interrupted: idle, but a check or a repair stopped before its end
	// (md/sync_min keeps where; md sets it back to 0 when one completes).
	Interrupted bool `json:"interrupted,omitempty"`
}

// Rebuilding reports an array whose members are all there while md rebuilds
// one of them (a new array, or a disk just added): no redundancy until it
// ends, yet no disk is missing (seen on the Pi 5: [3/2] for a fresh RAID5).
func (m MDStatus) Rebuilding() bool {
	return m.Sync != nil && (m.Sync.Action == "recovery" || m.Sync.Action == "resync") &&
		m.Active < m.Expected && len(m.Members)-len(m.Faulty) >= m.Expected
}

// Broken reports an array that lost more members than its level tolerates (a
// hub cut took 2 of 3 RAID5 members on the Pi 5: "broken raid5"). It serves no
// data, and its members must never be taken back one by one: a --re-add or an
// --add could rewrite their roles. The array is assembled again as a whole.
func (m MDStatus) Broken() bool {
	return m.Expected > 0 && m.Expected-m.Active > MDTolerance(m.Level, m.Expected)
}

// MDTolerance is how many members an array of this level can lose and still
// serve its data. Unknown level: 0 (fail closed).
func MDTolerance(level string, members int) int {
	switch level {
	case "raid1":
		return members - 1
	case "raid4", "raid5", "raid10":
		return 1
	case "raid6":
		return 2
	}
	return 0
}

// Degraded reports missing members.
func (m MDStatus) Degraded() bool {
	return (m.Expected > 0 && m.Active < m.Expected) || len(m.Faulty) > 0
}

// PoolStatus is the live state of a pool (API: GET /storage/pools/{name}).
type PoolStatus struct {
	Name       string `json:"name"`
	Layout     string `json:"layout"`
	UUID       string `json:"uuid"`
	MountPoint string `json:"mountPoint"`
	Mounted    bool   `json:"mounted"`
	Health     string `json:"health"`
	SizeBytes  uint64 `json:"sizeBytes"`
	UsedBytes  uint64 `json:"usedBytes"`
	FreeBytes  uint64 `json:"freeBytes"`
	// LogicalUsedBytes is what the files occupy once (data + metadata, without
	// the RAID copies); UsableBytes = LogicalUsedBytes + FreeBytes is the
	// capacity as the user understands it.
	LogicalUsedBytes uint64         `json:"logicalUsedBytes"`
	UsableBytes      uint64         `json:"usableBytes"`
	Missing          uint64         `json:"missingBytes"`
	Slack            uint64         `json:"slackBytes"` // partition space the filesystem does not use yet
	Devices          []PoolDevice   `json:"devices"`
	Profiles         []ProfileUsage `json:"profiles"`
	MD               *MDStatus      `json:"md"`
	Scrub            ScrubStatus    `json:"scrub"`

	// Failure handling (ADR-0012).
	MissingDevices   int            `json:"missingDevices"`   // expected disks that are absent
	MountedDegraded  bool           `json:"mountedDegraded"`  // mounted with -o degraded
	CanMountDegraded bool           `json:"canMountDegraded"` // unmounted because a disk is missing
	MixedProfiles    bool           `json:"mixedProfiles"`    // chunks left without redundancy
	Replace          *ReplaceStatus `json:"replace"`
	Balance          *BalanceStatus `json:"balance"`

	// A disk back after being unplugged (ADR-0027).
	CanResume    bool `json:"canResume"`    // a member is present but unused until the pool is remounted
	ResyncNeeded bool `json:"resyncNeeded"` // degraded since the last scrub: copies not checked (set by the daemon)
	RepairNeeded bool `json:"repairNeeded"` // forced assembly: md owes a parity repair, before that scrub (set by the daemon)

	// A parity pool that lost more disks than it tolerates (ADR-0032).
	CanReassemble bool        `json:"canReassemble"` // every member is back: a forced assembly can restart it
	Reassemble    *Reassemble `json:"reassemble"`    // set when its array is broken or not started
}

// ReassembleMaxEventsGap: beyond this many superblock updates behind the
// freshest member, forcing a member back is left to a manual procedure.
const ReassembleMaxEventsGap = 50

// Why a parity pool cannot be reassembled (Reassemble.Reason).
const (
	ReassembleMissingDisks = "missing_disks" // fewer members found than the array has
	ReassembleRoles        = "roles"         // two members claim a role, or one from another array
	ReassembleEventsGap    = "events_gap"    // a member is too far behind: manual procedure
)

// Reassemble describes the members of a parity pool whose array is broken or
// was not started (ADR-0032), as their own superblocks tell.
type Reassemble struct {
	Level     string   `json:"level"`
	Expected  int      `json:"expected"`
	Members   []string `json:"members"`   // kernel names of the partitions found
	Fresh     int      `json:"fresh"`     // members with the freshest superblock
	EventsGap int64    `json:"eventsGap"` // freshest minus stalest
	// Reason it cannot be reassembled: "" (it can), ReassembleMissingDisks,
	// ReassembleRoles, ReassembleEventsGap.
	Reason string `json:"reason"`
}

// MDExamine is what `mdadm --examine <partition>` reads from a member's own
// superblock: unlike /proc/mdstat, it still knows its role in the array.
type MDExamine struct {
	ArrayUUID   string
	Level       string
	RaidDevices int
	Events      int64
	Role        int // -1: spare, or unknown
}

// ParseMDExamine parses the text form of `mdadm --examine` (the --export form
// has no role). ok is false without an array UUID or an event count.
func ParseMDExamine(text string) (MDExamine, bool) {
	e := MDExamine{Role: -1}
	var haveEvents bool
	for _, line := range strings.Split(text, "\n") {
		key, value, ok := strings.Cut(line, " : ")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "Array UUID":
			e.ArrayUUID = value
		case "Raid Level":
			e.Level = value
		case "Raid Devices":
			// Unreadable: 0, which no role fits, so the check refuses (fail closed).
			e.RaidDevices, _ = strconv.Atoi(value)
		case "Events":
			n, err := strconv.ParseInt(value, 10, 64)
			e.Events, haveEvents = n, err == nil
		case "Device Role":
			if n, ok := strings.CutPrefix(value, "Active device "); ok {
				if r, err := strconv.Atoi(n); err == nil {
					e.Role = r
				}
			}
		}
	}
	return e, e.ArrayUUID != "" && haveEvents
}

// ComputeUsage derives the logical used space and the usable capacity.
func (s *PoolStatus) ComputeUsage() {
	s.LogicalUsedBytes = 0
	for _, p := range s.Profiles {
		if p.Type == "Data" || p.Type == "Metadata" || p.Type == "System" {
			s.LogicalUsedBytes += p.Used
		}
	}
	s.UsableBytes = s.LogicalUsedBytes + s.FreeBytes
}

// ComputeHealth sets Health from the collected facts.
func (s *PoolStatus) ComputeHealth() {
	switch {
	case !s.Mounted:
		s.Health = HealthUnmounted
	case s.Missing > 0 || s.MissingDevices > 0 || s.MixedProfiles || (s.MD != nil && s.MD.Degraded()) || s.ResyncNeeded || s.RepairNeeded:
		s.Health = HealthDegraded
	case s.hasErrors():
		s.Health = HealthErrors
	default:
		s.Health = HealthOK
	}
}

// ParityFailed reports a parity pool seen beyond what its array tolerates:
// broken while running, or every disk back but the array held after a reboot.
// Writes cut at the failure may have left a stripe's parity wrong: whatever
// brings the pool back (the button, a manual recovery), it owes a parity
// repair, then a scrub (ADR-0032).
func (s *PoolStatus) ParityFailed() bool {
	return (s.MD != nil && s.MD.Broken()) || s.CanReassemble
}

func (s *PoolStatus) hasErrors() bool {
	for _, d := range s.Devices {
		if d.Errors.Total() > 0 {
			return true
		}
	}
	return s.Scrub.Uncorrectable > 0
}

// ParseDeviceStats parses `btrfs --format json device stats <mnt>`.
func ParseDeviceStats(data []byte) ([]PoolDevice, error) {
	var out struct {
		Stats []struct {
			Device     string `json:"device"`
			DevID      int    `json:"devid"`
			Write      int64  `json:"write_io_errs"`
			Read       int64  `json:"read_io_errs"`
			Flush      int64  `json:"flush_io_errs"`
			Corruption int64  `json:"corruption_errs"`
			Generation int64  `json:"generation_errs"`
		} `json:"device-stats"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("device stats: %w", err)
	}
	devs := make([]PoolDevice, 0, len(out.Stats))
	for _, s := range out.Stats {
		devs = append(devs, PoolDevice{Path: s.Device, DevID: s.DevID, Errors: DeviceErrors{
			Write: s.Write, Read: s.Read, Flush: s.Flush, Corruption: s.Corruption, Generation: s.Generation,
		}})
	}
	return devs, nil
}

// ParseFilesystemDF parses `btrfs --format json filesystem df <mnt>`.
func ParseFilesystemDF(data []byte) ([]ProfileUsage, error) {
	var out struct {
		DF []struct {
			Type    string `json:"bg-type"`
			Profile string `json:"bg-profile"`
			Total   uint64 `json:"total"`
			Used    uint64 `json:"used"`
		} `json:"filesystem-df"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("filesystem df: %w", err)
	}
	usage := make([]ProfileUsage, 0, len(out.DF))
	for _, d := range out.DF {
		usage = append(usage, ProfileUsage{Type: d.Type, Profile: d.Profile, Total: d.Total, Used: d.Used})
	}
	return usage, nil
}

var usageLineRe = regexp.MustCompile(`^\s*([A-Za-z ()]+):\s+(\d+)`)

// ParseFilesystemUsage extracts sizes from `btrfs filesystem usage -b <mnt>`
// (no JSON output in btrfs-progs 6.14). Size and used are raw device bytes;
// free is the estimate that accounts for the RAID profile.
func ParseFilesystemUsage(text string) (size, used, free, missing uint64) {
	for _, line := range strings.Split(text, "\n") {
		m := usageLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		v, err := strconv.ParseUint(m[2], 10, 64)
		if err != nil {
			continue
		}
		switch strings.TrimSpace(m[1]) {
		case "Device size":
			size = v
		case "Used":
			used = v
		case "Free (estimated)":
			free = v
		case "Device missing":
			missing = v
		}
	}
	return size, used, free, missing
}

// ParseDeviceSlack reads "Device slack" from `btrfs filesystem usage -b`: the
// partition space the filesystem does not use. A disk replaced by a bigger one
// keeps the old size until a resize (seen on the Pi 5: 1.78 GB after a 128 GB
// stick replaced a smaller "128 GB" one).
func ParseDeviceSlack(text string) uint64 {
	for _, line := range strings.Split(text, "\n") {
		if m := usageLineRe.FindStringSubmatch(line); m != nil && strings.TrimSpace(m[1]) == "Device slack" {
			if v, err := strconv.ParseUint(m[2], 10, 64); err == nil {
				return v
			}
		}
	}
	return 0
}

// ParseScrubStatus parses `btrfs scrub status -R <mnt>`.
func ParseScrubStatus(text string) ScrubStatus {
	s := ScrubStatus{Status: "never"}
	for _, line := range strings.Split(text, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		n, _ := strconv.ParseInt(value, 10, 64)
		switch key {
		case "Status":
			s.Status = value
		case "Scrub started", "Scrub resumed":
			s.Started, s.Resumed = value, key == "Scrub resumed"
			// btrfs prints ctime-style local time: "Thu Oct  1 21:59:52 2026".
			if t, err := time.ParseInLocation(time.ANSIC, value, time.Local); err == nil {
				s.StartedAt = t.Unix()
			}
		case "Duration":
			s.Duration = value
			s.DurationSeconds = parseHMS(value)
		case "corrected_errors":
			s.Corrected = n
		case "uncorrectable_errors":
			s.Uncorrectable = n
		case "csum_errors":
			s.CsumErrors = n
		case "read_errors":
			s.ReadErrors = n
		}
	}
	return s
}

// ParseMDExport parses `mdadm --detail --export` (KEY=VALUE lines).
func ParseMDExport(text string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			out[k] = v
		}
	}
	return out
}

var (
	mdstatHeadRe   = regexp.MustCompile(`^(md\d+)\s*:\s*(\S+)\s+(.*)$`)
	mdstatCountRe  = regexp.MustCompile(`\[(\d+)/(\d+)\]`)
	mdstatSyncRe   = regexp.MustCompile(`(resync|recovery|check|reshape|repair)\s*=\s*([\d.]+)%`)
	mdstatMemberRe = regexp.MustCompile(`^(\S+?)\[\d+\]`)
)

// ParseMDStat parses /proc/mdstat, keyed by kernel name (md127).
func ParseMDStat(text string) map[string]MDStatus {
	arrays := map[string]MDStatus{}
	current := ""
	for _, line := range strings.Split(text, "\n") {
		if m := mdstatHeadRe.FindStringSubmatch(line); m != nil {
			// "md127 : active raid5 sda1[0] …", "md127 : active (auto-read-only)
			// raid5 …", "md127 : broken raid5 sda1[0]", "md127 : inactive
			// sdd1[3](S) …" (no level: members held, not started).
			current = m[1]
			st := MDStatus{Device: "/dev/" + m[1], State: m[2]}
			fields := strings.Fields(m[3])
			for len(fields) > 0 && strings.HasPrefix(fields[0], "(") {
				fields = fields[1:]
			}
			if st.State != "inactive" && len(fields) > 0 && mdstatMemberRe.FindStringSubmatch(fields[0]) == nil {
				st.Level, fields = fields[0], fields[1:]
			}
			for _, f := range fields {
				if mm := mdstatMemberRe.FindStringSubmatch(f); mm != nil {
					st.Members = append(st.Members, mm[1])
					if strings.HasSuffix(f, "(F)") {
						st.Faulty = append(st.Faulty, mm[1])
					}
				}
			}
			arrays[current] = st
			continue
		}
		if current == "" {
			continue
		}
		st := arrays[current]
		if m := mdstatCountRe.FindStringSubmatch(line); m != nil && st.Expected == 0 {
			st.Expected, _ = strconv.Atoi(m[1])
			st.Active, _ = strconv.Atoi(m[2])
		}
		if m := mdstatSyncRe.FindStringSubmatch(line); m != nil {
			pct, _ := strconv.ParseFloat(m[2], 64)
			st.Sync = &MDSync{Action: m[1], Percent: pct}
		}
		arrays[current] = st
		if strings.TrimSpace(line) == "" {
			current = "" // a blank line ends the array's block
		}
	}
	return arrays
}

// parseHMS converts "H:MM:SS" (hours may exceed 24) to seconds, 0 on error.
func parseHMS(v string) int64 {
	parts := strings.Split(v, ":")
	if len(parts) != 3 {
		return 0
	}
	var total int64
	for _, p := range parts {
		n, err := strconv.ParseInt(p, 10, 64)
		if err != nil || n < 0 {
			return 0
		}
		total = total*60 + n
	}
	return total
}
