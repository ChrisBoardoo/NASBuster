// SPDX-License-Identifier: AGPL-3.0-or-later

// Package system reads the state of the host: model, memory, CPU, temperature,
// power/throttling flags, boot device, OS and timezone.
//
// Every probe reads through an fs.FS rooted at "/" (os.DirFS("/") in production),
// so tests and demo mode can substitute a fixture tree of /proc, /sys and /etc.
// A probe that cannot answer returns ok=false; it never fails the whole report.
package system

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io/fs"
	"path"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/crosswax/nasbuster/internal/sysexec"
	"github.com/crosswax/nasbuster/internal/validate"
)

// Prober reads system state.
type Prober struct {
	FS     fs.FS          // system root
	Run    sysexec.Runner // used only for vcgencmd when sysfs has no throttling file
	GOARCH string         // fallback when the kernel does not expose its arch
	// ClockSynced tells whether the system clock follows network time
	// (KernelClockSynced on the NAS). nil: no clock check.
	ClockSynced func() (synced, known bool)
}

// New returns a Prober over root.
func New(root fs.FS, run sysexec.Runner) *Prober {
	return &Prober{FS: root, Run: run, GOARCH: runtime.GOARCH}
}

// BootDevice describes the device holding the root filesystem.
type BootDevice struct {
	Kind   string `json:"kind"` // sd_card, ssd, other, unknown
	Device string `json:"device"`
}

// Info is the static description of the host (API: GET /system/info).
type Info struct {
	Hostname      string     `json:"hostname"`
	Model         string     `json:"model"`
	IsRaspberryPi bool       `json:"isRaspberryPi"`
	OS            string     `json:"os"`
	Kernel        string     `json:"kernel"`
	Arch          string     `json:"arch"`
	CPUCores      int        `json:"cpuCores"`
	MemTotalBytes uint64     `json:"memTotalBytes"`
	UptimeSeconds int64      `json:"uptimeSeconds"`
	Timezone      string     `json:"timezone"`
	BootDevice    BootDevice `json:"bootDevice"`
}

// Info gathers the host description. Unknown fields stay empty.
func (p *Prober) Info() Info {
	info := Info{
		Hostname: p.readTrim("proc/sys/kernel/hostname"),
		Model:    p.Model(),
		OS:       p.OSName(),
		Kernel:   p.readTrim("proc/sys/kernel/osrelease"),
		Arch:     p.Arch(),
		Timezone: p.Timezone(),
	}
	info.IsRaspberryPi = strings.HasPrefix(info.Model, "Raspberry Pi")
	if _, cores, err := p.CPUTimes(); err == nil {
		info.CPUCores = cores
	}
	if total, _, err := p.MemInfo(); err == nil {
		info.MemTotalBytes = total
	}
	if up, ok := p.Uptime(); ok {
		info.UptimeSeconds = int64(up)
	}
	info.BootDevice = p.BootDevice()
	return info
}

func (p *Prober) readTrim(name string) string {
	b, err := fs.ReadFile(p.FS, name)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.TrimRight(string(b), "\x00\n"))
}

// readU32 reads a device-tree cell: a big-endian unsigned 32-bit integer.
func (p *Prober) readU32(name string) (uint32, bool) {
	b, err := fs.ReadFile(p.FS, name)
	if err != nil || len(b) < 4 {
		return 0, false
	}
	return binary.BigEndian.Uint32(b[:4]), true
}

// Model returns the device-tree model ("Raspberry Pi 4 Model B Rev 1.5"),
// falling back to DMI on PCs and VMs.
func (p *Prober) Model() string {
	for _, name := range []string{"proc/device-tree/model", "sys/firmware/devicetree/base/model"} {
		if m := strings.Trim(p.readTrim(name), "\x00 "); m != "" {
			return m
		}
	}
	vendor := p.readTrim("sys/class/dmi/id/sys_vendor")
	product := p.readTrim("sys/class/dmi/id/product_name")
	return strings.TrimSpace(vendor + " " + product)
}

// OSName returns PRETTY_NAME from os-release.
func (p *Prober) OSName() string {
	for _, name := range []string{"etc/os-release", "usr/lib/os-release"} {
		b, err := fs.ReadFile(p.FS, name)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			if v, ok := strings.CutPrefix(line, "PRETTY_NAME="); ok {
				return strings.Trim(v, `"'`)
			}
		}
	}
	return ""
}

// Arch returns the machine name as uname -m would ("aarch64", "x86_64").
func (p *Prober) Arch() string {
	if a := p.readTrim("proc/sys/kernel/arch"); a != "" {
		return a
	}
	switch p.GOARCH {
	case "arm64":
		return "aarch64"
	case "amd64":
		return "x86_64"
	case "arm":
		return "armv7l"
	case "386":
		return "i686"
	}
	return p.GOARCH
}

// MemInfo returns MemTotal and MemAvailable in bytes.
func (p *Prober) MemInfo() (total, available uint64, err error) {
	b, err := fs.ReadFile(p.FS, "proc/meminfo")
	if err != nil {
		return 0, 0, err
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		v, perr := strconv.ParseUint(fields[1], 10, 64)
		if perr != nil {
			continue
		}
		if len(fields) == 3 && fields[2] == "kB" {
			v *= 1024
		}
		switch fields[0] {
		case "MemTotal:":
			total = v
		case "MemAvailable:":
			available = v
		}
	}
	if total == 0 {
		return 0, 0, errors.New("meminfo: no MemTotal")
	}
	return total, available, nil
}

// CPUTimes holds the aggregate jiffies from /proc/stat.
type CPUTimes struct {
	Idle  uint64 // idle + iowait
	Total uint64
}

// CPUTimes returns aggregate CPU counters and the number of cores.
func (p *Prober) CPUTimes() (CPUTimes, int, error) {
	b, err := fs.ReadFile(p.FS, "proc/stat")
	if err != nil {
		return CPUTimes{}, 0, err
	}
	var t CPUTimes
	cores := 0
	found := false
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || !strings.HasPrefix(fields[0], "cpu") {
			continue
		}
		if fields[0] != "cpu" {
			cores++
			continue
		}
		found = true
		// user nice system idle iowait irq softirq steal (guest columns are already in user/nice)
		for i, f := range fields[1:] {
			if i >= 8 {
				break
			}
			v, perr := strconv.ParseUint(f, 10, 64)
			if perr != nil {
				return CPUTimes{}, 0, perr
			}
			t.Total += v
			if i == 3 || i == 4 {
				t.Idle += v
			}
		}
	}
	if !found {
		return CPUTimes{}, 0, errors.New("stat: no aggregate cpu line")
	}
	return t, cores, nil
}

// Load1 returns the 1-minute load average.
func (p *Prober) Load1() (float64, bool) {
	fields := strings.Fields(p.readTrim("proc/loadavg"))
	if len(fields) == 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(fields[0], 64)
	return v, err == nil
}

// Uptime returns seconds since boot.
func (p *Prober) Uptime() (float64, bool) {
	fields := strings.Fields(p.readTrim("proc/uptime"))
	if len(fields) == 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(fields[0], 64)
	return v, err == nil
}

// Temperature returns the SoC temperature in °C.
func (p *Prober) Temperature() (float64, bool) {
	zones, _ := fs.Glob(p.FS, "sys/class/thermal/thermal_zone*")
	sort.Strings(zones)
	best := ""
	for _, z := range zones {
		switch p.readTrim(path.Join(z, "type")) {
		case "cpu-thermal", "x86_pkg_temp", "soc-thermal":
			best = z
		}
		if best != "" {
			break
		}
	}
	if best == "" && len(zones) > 0 {
		best = zones[0]
	}
	if best == "" {
		return 0, false
	}
	milli, err := strconv.ParseInt(p.readTrim(path.Join(best, "temp")), 10, 64)
	if err != nil {
		return 0, false
	}
	return float64(milli) / 1000, true
}

// Throttling bits reported by the Raspberry Pi firmware.
const (
	UnderVoltageNow       = 1 << 0
	FreqCappedNow         = 1 << 1
	ThrottledNow          = 1 << 2
	SoftTempLimitNow      = 1 << 3
	UnderVoltageOccurred  = 1 << 16
	FreqCappedOccurred    = 1 << 17
	ThrottledOccurred     = 1 << 18
	SoftTempLimitOccurred = 1 << 19
)

var vcgencmdThrottledRe = regexp.MustCompile(`throttled=0x([0-9a-fA-F]+)`)

// Throttled returns the firmware throttling bitmask. It reads sysfs first
// (no exec, no privileges) and falls back to `vcgencmd get_throttled`.
func (p *Prober) Throttled(ctx context.Context) (uint32, bool) {
	if s := p.readTrim("sys/devices/platform/soc/soc:firmware/get_throttled"); s != "" {
		if v, err := strconv.ParseUint(strings.TrimPrefix(s, "0x"), 16, 32); err == nil {
			return uint32(v), true
		}
	}
	if p.Run == nil {
		return 0, false
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	res, err := p.Run.Run(ctx, "vcgencmd", "get_throttled")
	if err != nil || res.ExitCode != 0 {
		return 0, false
	}
	m := vcgencmdThrottledRe.FindSubmatch(res.Stdout)
	if m == nil {
		return 0, false
	}
	v, err := strconv.ParseUint(string(m[1]), 16, 32)
	if err != nil {
		return 0, false
	}
	return uint32(v), true
}

var (
	partSuffixRe  = regexp.MustCompile(`^((?:mmcblk|nvme\d+n|loop)\d+)p\d+$`)
	sdPartitionRe = regexp.MustCompile(`^((?:sd|vd|xvd|hd)[a-z]+)\d+$`)
)

// diskOf maps a partition name to its parent disk name.
func diskOf(part string) string {
	if m := partSuffixRe.FindStringSubmatch(part); m != nil {
		return m[1]
	}
	if m := sdPartitionRe.FindStringSubmatch(part); m != nil {
		return m[1]
	}
	return part
}

// BootDevice finds the disk holding "/" from /proc/self/mountinfo.
func (p *Prober) BootDevice() BootDevice {
	b, err := fs.ReadFile(p.FS, "proc/self/mountinfo")
	if err != nil {
		return BootDevice{Kind: "unknown"}
	}
	var majMin, source string
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 || fields[4] != "/" {
			continue
		}
		sep := -1
		for i, f := range fields {
			if f == "-" {
				sep = i
				break
			}
		}
		if sep < 0 || sep+2 >= len(fields) {
			continue
		}
		// Keep the last match: later lines over-mount earlier ones.
		majMin, source = fields[2], fields[sep+2]
	}
	name := ""
	if strings.HasPrefix(source, "/dev/") && source != "/dev/root" {
		name = strings.TrimPrefix(source, "/dev/")
	} else if majMin != "" {
		for _, line := range strings.Split(p.readTrim("sys/dev/block/"+majMin+"/uevent"), "\n") {
			if v, ok := strings.CutPrefix(line, "DEVNAME="); ok {
				name = v
			}
		}
	}
	if name == "" || strings.Contains(name, "/") {
		return BootDevice{Kind: "unknown"}
	}
	disk := diskOf(name)
	switch {
	case strings.HasPrefix(disk, "mmcblk"):
		return BootDevice{Kind: "sd_card", Device: disk}
	case strings.HasPrefix(disk, "nvme"):
		return BootDevice{Kind: "ssd", Device: disk}
	case strings.HasPrefix(disk, "sd"):
		if p.readTrim("sys/block/"+disk+"/queue/rotational") == "0" {
			return BootDevice{Kind: "ssd", Device: disk}
		}
		return BootDevice{Kind: "other", Device: disk}
	default:
		return BootDevice{Kind: "other", Device: disk}
	}
}

// Timezone returns the configured IANA zone, "" if unknown.
func (p *Prober) Timezone() string {
	if rl, ok := p.FS.(fs.ReadLinkFS); ok {
		if target, err := rl.ReadLink("etc/localtime"); err == nil {
			if _, zone, found := strings.Cut(target, "zoneinfo/"); found && validate.Timezone(zone) {
				return zone
			}
		}
	}
	if tz := p.readTrim("etc/timezone"); validate.Timezone(tz) {
		return tz
	}
	return ""
}

// Timezones lists the zones offered to the user (zone.tab ∪ zone1970.tab, plus UTC).
func (p *Prober) Timezones() []string {
	set := map[string]bool{"UTC": true}
	for _, tab := range []string{"usr/share/zoneinfo/zone.tab", "usr/share/zoneinfo/zone1970.tab"} {
		b, err := fs.ReadFile(p.FS, tab)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(line, "#") {
				continue
			}
			fields := strings.Split(line, "\t")
			if len(fields) >= 3 && validate.Timezone(fields[2]) {
				set[fields[2]] = true
			}
		}
	}
	out := make([]string, 0, len(set))
	for tz := range set {
		out = append(out, tz)
	}
	sort.Strings(out)
	return out
}

// TimezoneExists checks that the zone is syntactically valid and installed.
func (p *Prober) TimezoneExists(tz string) bool {
	if !validate.Timezone(tz) {
		return false
	}
	info, err := fs.Stat(p.FS, "usr/share/zoneinfo/"+tz)
	return err == nil && info.Mode().IsRegular()
}
