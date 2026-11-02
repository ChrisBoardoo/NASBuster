// SPDX-License-Identifier: AGPL-3.0-or-later

package system

import (
	"context"
	"testing"

	"github.com/crosswax/nasbuster/internal/fixture"
	"github.com/crosswax/nasbuster/internal/sysexec"
)

// pi4 is a Raspberry Pi 4 8 GB booting from an SD card. get_throttled = 0x50000:
// under-voltage (bit 16) and throttling (bit 18) occurred since boot.
const pi4 = `
-- proc/device-tree/model --
Raspberry Pi 4 Model B Rev 1.5` + "\x00" + `
-- proc/meminfo --
MemTotal:        7997684 kB
MemFree:         6512340 kB
MemAvailable:    7123456 kB
-- proc/stat --
cpu  1000 0 500 8000 500 0 0 0 0 0
cpu0 250 0 125 2000 125 0 0 0 0 0
cpu1 250 0 125 2000 125 0 0 0 0 0
cpu2 250 0 125 2000 125 0 0 0 0 0
cpu3 250 0 125 2000 125 0 0 0 0 0
intr 0
-- proc/loadavg --
0.31 0.25 0.20 1/180 1234
-- proc/uptime --
93784.12 360000.00
-- proc/self/mountinfo --
22 1 179:2 / / rw,noatime shared:1 - ext4 /dev/mmcblk0p2 rw
30 22 179:1 / /boot/firmware rw,relatime shared:7 - vfat /dev/mmcblk0p1 rw
-- proc/sys/kernel/hostname --
nasbuster
-- proc/sys/kernel/osrelease --
6.12.47+rpt-rpi-v8
-- etc/os-release --
PRETTY_NAME="Debian GNU/Linux 13 (trixie)"
NAME="Debian GNU/Linux"
-- etc/localtime -> /usr/share/zoneinfo/Europe/Paris --
-- usr/share/zoneinfo/Europe/Paris --
TZif
-- usr/share/zoneinfo/zone.tab --
# comment
FR	+4852+00220	Europe/Paris
BE	+5050+00420	Europe/Brussels
-- usr/share/zoneinfo/zone1970.tab --
FR,MC	+4852+00220	Europe/Paris
-- sys/class/thermal/thermal_zone0/type --
cpu-thermal
-- sys/class/thermal/thermal_zone0/temp --
47234
-- sys/devices/platform/soc/soc:firmware/get_throttled --
50000
`

func newPi4(t *testing.T) *Prober {
	t.Helper()
	return &Prober{FS: fixture.Parse([]byte(pi4)), Run: sysexec.NewFake(), GOARCH: "arm64"}
}

func TestInfo(t *testing.T) {
	info := newPi4(t).Info()
	want := Info{
		Hostname:      "nasbuster",
		Model:         "Raspberry Pi 4 Model B Rev 1.5",
		IsRaspberryPi: true,
		OS:            "Debian GNU/Linux 13 (trixie)",
		Kernel:        "6.12.47+rpt-rpi-v8",
		Arch:          "aarch64",
		CPUCores:      4,
		MemTotalBytes: 7997684 * 1024,
		UptimeSeconds: 93784,
		Timezone:      "Europe/Paris",
		BootDevice:    BootDevice{Kind: "sd_card", Device: "mmcblk0"},
	}
	if info != want {
		t.Fatalf("Info() =\n%+v\nwant\n%+v", info, want)
	}
}

func TestChecks(t *testing.T) {
	got := map[string]Check{}
	for _, c := range newPi4(t).Checks(context.Background()) {
		got[c.ID] = c
	}
	want := map[string]struct {
		status Status
		code   string
	}{
		"model":       {StatusOK, "model.raspberry_pi"},
		"arch":        {StatusOK, "arch.64bit"},
		"memory":      {StatusOK, "memory.ok"},
		"temperature": {StatusOK, "temperature.ok"},
		"power":       {StatusWarn, "power.under_voltage_occurred"},
		"throttling":  {StatusWarn, "throttling.occurred"}, // 0x50000 = bits 16 + 18
		"bootDevice":  {StatusWarn, "bootDevice.sd_card"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d checks, want %d", len(got), len(want))
	}
	for id, w := range want {
		if got[id].Status != w.status || got[id].Code != w.code {
			t.Errorf("%s = %s/%s, want %s/%s", id, got[id].Status, got[id].Code, w.status, w.code)
		}
		if got[id].Params == nil {
			t.Errorf("%s: params must never be nil (JSON {})", id)
		}
	}
	if c := got["temperature"].Params["celsius"]; c != 47.2 {
		t.Errorf("celsius = %v, want 47.2", c)
	}
}

func TestThrottledFallsBackToVcgencmd(t *testing.T) {
	fake := sysexec.NewFake().Set(sysexec.Result{Stdout: []byte("throttled=0x5\n")}, "vcgencmd", "get_throttled")
	p := &Prober{FS: fixture.Parse(nil), Run: fake}
	flags, ok := p.Throttled(context.Background())
	if !ok || flags != UnderVoltageNow|ThrottledNow {
		t.Fatalf("Throttled() = %#x, %v", flags, ok)
	}
	if c := powerCheck(flags, ok); c.Status != StatusCrit {
		t.Errorf("power status = %s, want crit", c.Status)
	}
	if c := throttlingCheck(flags, ok); c.Code != "throttling.active" {
		t.Errorf("throttling code = %s", c.Code)
	}
}

func TestUnknownHost(t *testing.T) {
	p := &Prober{FS: fixture.Parse(nil), Run: sysexec.NewFake(), GOARCH: "amd64"}
	for _, c := range p.Checks(context.Background()) {
		if c.ID == "arch" {
			if c.Status != StatusOK {
				t.Errorf("arch fallback: %+v", c)
			}
			continue
		}
		if c.Status != StatusUnknown {
			t.Errorf("%s on empty system = %s, want unknown", c.ID, c.Status)
		}
	}
}

func TestBootDeviceUSBSSD(t *testing.T) {
	p := &Prober{FS: fixture.Parse([]byte(`
-- proc/self/mountinfo --
22 1 0:26 / / rw,noatime shared:1 - btrfs /dev/sda2 rw,subvol=/@
-- sys/block/sda/queue/rotational --
0
`))}
	if got := p.BootDevice(); got != (BootDevice{Kind: "ssd", Device: "sda"}) {
		t.Fatalf("BootDevice() = %+v", got)
	}
}

func TestBootDeviceDevRoot(t *testing.T) {
	p := &Prober{FS: fixture.Parse([]byte(`
-- proc/self/mountinfo --
22 1 259:2 / / rw shared:1 - ext4 /dev/root rw
-- sys/dev/block/259:2/uevent --
MAJOR=259
MINOR=2
DEVNAME=nvme0n1p2
DEVTYPE=partition
`))}
	if got := p.BootDevice(); got != (BootDevice{Kind: "ssd", Device: "nvme0n1"}) {
		t.Fatalf("BootDevice() = %+v", got)
	}
}

func TestTimezones(t *testing.T) {
	p := newPi4(t)
	got := p.Timezones()
	want := []string{"Europe/Brussels", "Europe/Paris", "UTC"}
	if len(got) != len(want) {
		t.Fatalf("Timezones() = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Timezones() = %v, want %v", got, want)
		}
	}
	if !p.TimezoneExists("Europe/Paris") || p.TimezoneExists("Europe/Brussels") || p.TimezoneExists("../etc/passwd") {
		t.Error("TimezoneExists")
	}
}

func TestCPUTimes(t *testing.T) {
	times, cores, err := newPi4(t).CPUTimes()
	if err != nil || cores != 4 || times.Total != 10000 || times.Idle != 8500 {
		t.Fatalf("CPUTimes() = %+v, %d, %v", times, cores, err)
	}
}
