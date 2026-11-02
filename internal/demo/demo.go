// SPDX-License-Identifier: AGPL-3.0-or-later

// Package demo emulates a Raspberry Pi 4 NAS for UI development, screenshots
// and tests: a living /proc and /sys tree, and canned or simulated outputs for
// lsblk, udevadm, smartctl, hostnamectl, timedatectl and the pool tools
// (sgdisk, mdadm, mkfs.btrfs, btrfs, systemctl, findmnt). Nothing touches the
// real system. ⚠️ The data is synthetic; replace it with captures from the test Pi.
package demo

import (
	"embed"
	"fmt"
	"io/fs"
	"maps"
	"math"
	"path"
	"strings"
	"sync"
	"testing/fstest"
	"time"

	"github.com/crosswax/nasbuster/internal/fixture"
	"github.com/crosswax/nasbuster/internal/storage"
	"github.com/crosswax/nasbuster/internal/sysexec"
	"github.com/crosswax/nasbuster/internal/validate"
)

//go:embed data
var data embed.FS

// System is one emulated Pi. All changes are kept in memory.
type System struct {
	FS     *FS
	Runner *sysexec.Fake
	Files  *Files // what the privileged helper writes (units, mdadm.conf)
	// Clock drives the generated /proc and /sys values (tests override it).
	Clock func() time.Time

	mu       sync.Mutex
	hostname string
	timezone string
	devices  map[string]bool   // /dev/disk/by-id paths that "exist"
	byID     map[string]string // /dev/disk/by-id path -> kernel name (sda)
	lsblk    []map[string]any  // base lsblk tree
	sim      *poolSim
	shares   *shareSim
	apps     *appsSim
}

// Machine account of the emulated Pi (the one a user creates in Raspberry Pi
// Imager), accepted to create the first admin in demo mode (ADR-0018).
const (
	MachineUser     = "pi"
	MachinePassword = "raspberry"
)

// New returns a fresh emulated Pi.
func New() *System {
	s := &System{Clock: time.Now, hostname: "raspberrypi", timezone: "Europe/Paris",
		devices: map[string]bool{}, byID: map[string]string{}, Files: NewFiles()}
	s.FS = &FS{sys: s, base: fixture.Parse(mustRead("data/pi4-root.txtar")), start: time.Now()}
	s.Runner = sysexec.NewFake()
	// Raspberry Pi OS ships initramfs-tools: the helper rebuilds the initramfs
	// after mdadm.conf changes (ADR-0033).
	_ = s.Files.WriteFile(initramfsTool, nil, 0o755)
	s.sim = newPoolSim(s)
	s.shares = newShareSim(s)

	s.lsblk = parseLsblkTree(mustRead("data/lsblk.json"))
	for _, sec := range fixture.Sections(mustRead("data/udevadm.txtar")) {
		s.Runner.Set(sysexec.Result{Stdout: sec.Data}, "udevadm", storage.UdevArgs(sec.Name)...)
		kname := path.Base(sec.Name)
		for _, field := range strings.Fields(string(sec.Data)) {
			link := strings.TrimPrefix(field, "DEVLINKS=")
			if strings.HasPrefix(link, "/dev/disk/by-id/") {
				s.devices[link] = true
				s.byID[link] = kname
			}
		}
	}
	for _, sec := range fixture.Sections(mustRead("data/smartctl.txtar")) {
		id, deviceType, _ := strings.Cut(sec.Name, " ")
		res := sysexec.Result{Stdout: sec.Data, ExitCode: smartExit(sec.Data)}
		s.Runner.Set(res, "smartctl", storage.SmartArgs("/dev/disk/by-id/"+id, deviceType)...)
	}

	s.Runner.Handle("hostnamectl", func(args []string) (sysexec.Result, error) {
		if len(args) == 2 && args[0] == "set-hostname" && validate.Hostname(args[1]) {
			s.mu.Lock()
			s.hostname = args[1]
			s.mu.Unlock()
			return sysexec.Result{}, nil
		}
		return sysexec.Result{ExitCode: 1, Stderr: []byte("demo: unsupported hostnamectl call")}, nil
	})
	s.Runner.Handle("timedatectl", func(args []string) (sysexec.Result, error) {
		if len(args) == 2 && args[0] == "set-timezone" && validate.Timezone(args[1]) {
			s.mu.Lock()
			s.timezone = args[1]
			s.mu.Unlock()
			return sysexec.Result{}, nil
		}
		return sysexec.Result{ExitCode: 1, Stderr: []byte("demo: unsupported timedatectl call")}, nil
	})
	// The persistent journal (ADR-0031): its drop-in lands in the demo's files.
	s.Runner.Handle("systemd-tmpfiles", func(args []string) (sysexec.Result, error) {
		if len(args) == 3 && args[0] == "--create" && args[1] == "--prefix" && args[2] == "/var/log/journal" {
			return sysexec.Result{}, nil
		}
		return sysexec.Result{ExitCode: 1, Stderr: []byte("demo: unsupported systemd-tmpfiles call")}, nil
	})
	// The machine account that proves ownership for the first admin
	// (ADR-0018): "pi" is in the sudo group of the share simulator.
	s.Runner.HandleInput("unix_chkpwd", func(args []string, input []byte) (sysexec.Result, error) {
		if len(args) == 2 && args[0] == MachineUser && args[1] == "nonull" && string(input) == MachinePassword+"\x00" {
			return sysexec.Result{}, nil
		}
		return sysexec.Result{ExitCode: 7}, nil // PAM_AUTH_ERR, as the real checker
	})
	s.sim.register(s.Runner)
	s.shares.register(s.Runner)
	s.apps = newAppsSim(s)
	s.apps.register(s.Runner, s.sim.systemctl, s.sim.btrfs, s.sim.mdadm)
	return s
}

// DeviceExists reports whether a /dev/disk/by-id path exists in the demo.
func (s *System) DeviceExists(p string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.devices[p]
}

// WriteFile accepts and discards writes (the helper updates /etc/hosts).
func (s *System) WriteFile(string, []byte, fs.FileMode) error { return nil }

// WriteSysfs takes the md attributes the helper sets before a parity repair.
func (s *System) WriteSysfs(path string, value []byte) error { return s.sim.writeSysfs(path, value) }

func (s *System) state() (hostname, timezone string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hostname, s.timezone
}

// FS is the emulated root filesystem: a static base plus generated files.
type FS struct {
	sys   *System
	base  fstest.MapFS
	start time.Time
}

// snapshot builds the current tree. Cheap: the base has a few dozen entries.
func (f *FS) snapshot() fstest.MapFS {
	m := maps.Clone(f.base)
	t := f.sys.Clock().Sub(f.start).Seconds()
	file := func(name, content string) { m[name] = &fstest.MapFile{Data: []byte(content), Mode: 0o444} }
	link := func(name, target string) {
		m[name] = &fstest.MapFile{Data: []byte(target), Mode: fs.ModeSymlink | 0o777}
	}

	// 4 cores at 100 Hz. Busy fraction 0.12 + 0.08·sin(t/20), integrated.
	total := 400 * t
	busy := 400 * (0.12*t + 1.6*(1-math.Cos(t/20)))
	user, system, idle := busy*0.7, busy*0.3, total-busy
	var stat strings.Builder
	fmt.Fprintf(&stat, "cpu  %d 0 %d %d 0 0 0 0 0 0\n", int64(user), int64(system), int64(idle))
	for i := 0; i < 4; i++ {
		fmt.Fprintf(&stat, "cpu%d %d 0 %d %d 0 0 0 0 0 0\n", i, int64(user/4), int64(system/4), int64(idle/4))
	}
	file("proc/stat", stat.String())
	file("proc/meminfo", fmt.Sprintf("MemTotal:        7997684 kB\nMemFree:         6512340 kB\nMemAvailable:    %d kB\n",
		int64(7123456-40000*(1+math.Sin(t/45)))))
	file("proc/uptime", fmt.Sprintf("%.2f %.2f\n", 93784+t, 360000+3.5*t))
	// One boot per demo run: the event journal records it (ADR-0031).
	n := f.start.UnixNano()
	file("proc/sys/kernel/random/boot_id", fmt.Sprintf("%08x-%04x-4%03x-8%03x-%012x\n", uint32(n>>32), uint16(n>>16), n&0xfff, (n>>12)&0xfff, n&0xffffffffffff))
	file("proc/loadavg", fmt.Sprintf("%.2f 0.25 0.20 1/180 1234\n", 0.30+0.15*math.Sin(t/30)))
	file("sys/class/thermal/thermal_zone0/temp", fmt.Sprintf("%d\n", int64(47000+2500*math.Sin(t/25))))

	hostname, tz := f.sys.state()
	file("proc/sys/kernel/hostname", hostname+"\n")
	file("etc/timezone", tz+"\n")
	link("etc/localtime", "/usr/share/zoneinfo/"+tz)

	f.sys.sim.addFiles(file, link)
	return m
}

// Open implements fs.FS.
func (f *FS) Open(name string) (fs.File, error) { return f.snapshot().Open(name) }

// ReadLink implements fs.ReadLinkFS.
func (f *FS) ReadLink(name string) (string, error) { return f.snapshot().ReadLink(name) }

// Lstat implements fs.ReadLinkFS.
func (f *FS) Lstat(name string) (fs.FileInfo, error) { return f.snapshot().Lstat(name) }

func mustRead(name string) []byte {
	b, err := data.ReadFile(name)
	if err != nil {
		panic(err) // embedded at compile time
	}
	return b
}

// smartExit extracts smartctl's exit_status so the fake mirrors the real tool.
func smartExit(b []byte) int {
	const key = `"exit_status": `
	i := strings.Index(string(b), key)
	if i < 0 {
		return 0
	}
	n := 0
	for _, c := range string(b)[i+len(key):] {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}
