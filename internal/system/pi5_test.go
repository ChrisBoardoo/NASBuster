// SPDX-License-Identifier: AGPL-3.0-or-later

package system

import (
	"context"
	"encoding/binary"
	"os"
	"testing"
	"testing/fstest"

	"github.com/crosswax/nasbuster/internal/fixture"
	"github.com/crosswax/nasbuster/internal/sysexec"
)

// newPi5 is the Raspberry Pi 5 test unit, from files captured on the real board
// (testdata/pi5-root.txtar). Its kernel has no get_throttled file in sysfs, so
// the throttling flags come from vcgencmd, as captured: throttled=0x0.
func newPi5(t *testing.T) (*Prober, *sysexec.Fake) {
	t.Helper()
	data, err := os.ReadFile("testdata/pi5-root.txtar")
	if err != nil {
		t.Fatal(err)
	}
	fake := sysexec.NewFake().Set(sysexec.Result{Stdout: []byte("throttled=0x0\n")}, "vcgencmd", "get_throttled")
	return &Prober{FS: fixture.Parse(data), Run: fake, GOARCH: "arm64"}, fake
}

func TestInfoRealPi5(t *testing.T) {
	p, _ := newPi5(t)
	info := p.Info()
	want := Info{
		Hostname:      "Rpi5-NAS",
		Model:         "Raspberry Pi 5 Model B Rev 1.1",
		IsRaspberryPi: true,
		OS:            "Debian GNU/Linux 13 (trixie)",
		Kernel:        "6.18.50+rpt-rpi-2712",
		Arch:          "aarch64",
		CPUCores:      4,
		MemTotalBytes: 8255824 * 1024,
		UptimeSeconds: info.UptimeSeconds, // captured value, not the point here
		Timezone:      "Europe/Paris",
		BootDevice:    BootDevice{Kind: "sd_card", Device: "mmcblk0"},
	}
	if info != want {
		t.Fatalf("Info() =\n%+v\nwant\n%+v", info, want)
	}
	if info.UptimeSeconds <= 0 {
		t.Errorf("uptime = %d", info.UptimeSeconds)
	}
}

func TestChecksRealPi5(t *testing.T) {
	p, fake := newPi5(t)
	got := map[string]Check{}
	for _, c := range p.Checks(context.Background()) {
		got[c.ID] = c
	}
	want := map[string]string{
		"model":       "model.raspberry_pi",
		"arch":        "arch.64bit",
		"memory":      "memory.ok",
		"temperature": "temperature.ok",
		"power":       "power.ok",
		"throttling":  "throttling.none",
		"bootDevice":  "bootDevice.sd_card",
	}
	for id, code := range want {
		if got[id].Code != code {
			t.Errorf("%s = %s, want %s", id, got[id].Code, code)
		}
	}
	// The sysfs file is missing on the Pi 5: the answer must come from vcgencmd,
	// not from a silent "unknown".
	ran := false
	for _, c := range fake.Calls() {
		if len(c) == 2 && c[0] == "vcgencmd" && c[1] == "get_throttled" {
			ran = true
		}
	}
	if !ran {
		t.Error("vcgencmd get_throttled was not used")
	}
}

// u32 encodes a device-tree cell (big-endian), as the firmware exposes it.
func u32(v uint32) *fstest.MapFile {
	return &fstest.MapFile{Data: binary.BigEndian.AppendUint32(nil, v)}
}

func TestUSBPowerCheck(t *testing.T) {
	for _, tc := range []struct {
		name       string
		full, mA   uint32
		wantStatus Status
		wantCode   string
	}{
		// Captured on the test Pi with the official 27 W power supply.
		{"official 27 W supply", 1, 5000, StatusOK, "usbPower.full"},
		// A 15 W (3 A) supply: the firmware keeps the 600 mA USB limit.
		{"3 A supply", 0, 3000, StatusWarn, "usbPower.limited"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := newPi5(t)
			root := p.FS.(fstest.MapFS)
			root[dtUSBFullPower] = u32(tc.full)
			root[dtMaxCurrent] = u32(tc.mA)
			var got *Check
			for _, c := range p.Checks(context.Background()) {
				if c.ID == "usbPower" {
					got = &c
				}
			}
			if got == nil {
				t.Fatal("no usbPower check on a Pi 5")
			}
			if got.Status != tc.wantStatus || got.Code != tc.wantCode || got.Params["milliamps"] != tc.mA {
				t.Errorf("usbPower = %+v", *got)
			}
		})
	}
}

func TestUSBPowerCheckAbsentWithoutFirmwareFlag(t *testing.T) {
	p, _ := newPi5(t) // the capture has no chosen/power node: same as a Pi 4
	for _, c := range p.Checks(context.Background()) {
		if c.ID == "usbPower" {
			t.Fatalf("unexpected usbPower check: %+v", c)
		}
	}
}

func TestClockCheck(t *testing.T) {
	p, _ := newPi5(t)
	clock := func() (Check, bool) {
		for _, c := range p.Checks(context.Background()) {
			if c.ID == "clock" {
				return c, true
			}
		}
		return Check{}, false
	}
	if c, ok := clock(); ok {
		t.Fatalf("no clock source, yet a clock check: %+v", c)
	}
	for _, tc := range []struct {
		synced, known bool
		status        Status
		code          string
	}{
		{true, true, StatusOK, "clock.synced"},
		// Booted without network: the Pi restarts from its last shutdown time.
		{false, true, StatusWarn, "clock.not_synced"},
		{false, false, StatusUnknown, "clock.unknown"},
	} {
		p.ClockSynced = func() (bool, bool) { return tc.synced, tc.known }
		if c, ok := clock(); !ok || c.Status != tc.status || c.Code != tc.code {
			t.Errorf("synced=%v known=%v: %+v", tc.synced, tc.known, c)
		}
	}
}

func TestClockSyncedFallsBackToTimesyncd(t *testing.T) {
	// Tests run unsandboxed on Linux: adjtimex answers, the files are not read.
	if _, known := KernelClockSynced(); known {
		t.Skip("adjtimex readable here: the fallback is not used")
	}
	for name, tc := range map[string]struct {
		files         string
		synced, known bool
	}{
		"synchronized": {"-- run/systemd/timesync/synchronized --\n", true, true},
		"not yet":      {"-- run/systemd/timesync/clock --\n", false, true},
		"no timesyncd": {"-- etc/hostname --\npi\n", false, false},
	} {
		synced, known := ClockSynced(fixture.Parse([]byte(tc.files)))()
		if synced != tc.synced || known != tc.known {
			t.Errorf("%s: synced=%v known=%v", name, synced, known)
		}
	}
}
