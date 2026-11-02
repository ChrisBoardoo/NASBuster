// SPDX-License-Identifier: AGPL-3.0-or-later

package system

import (
	"context"
	"math"
)

// Status of a health check.
type Status string

// Check statuses, from best to worst ("info" and "unknown" are neutral).
const (
	StatusOK      Status = "ok"
	StatusInfo    Status = "info"
	StatusWarn    Status = "warn"
	StatusCrit    Status = "crit"
	StatusUnknown Status = "unknown"
)

// Check is one line of the "Pi health" report. Code is an i18n key suffix
// (the UI shows checks.<code>); Params carries raw values, never formatted text.
type Check struct {
	ID     string         `json:"id"`
	Status Status         `json:"status"`
	Code   string         `json:"code"`
	Params map[string]any `json:"params"`
}

// Thresholds. A "2 GB" board reports about 1.8 GiB of MemTotal.
const (
	memLowBytes     = 1536 << 20 // below: memory.low
	memVeryLowBytes = 768 << 20  // below: memory.very_low
	tempWarmC       = 70.0
	tempHotC        = 80.0
)

// Checks evaluates the host for the onboarding health step and the dashboard.
func (p *Prober) Checks(ctx context.Context) []Check {
	info := p.Info()
	checks := []Check{
		modelCheck(info),
		archCheck(info.Arch),
		memoryCheck(info.MemTotalBytes),
		p.temperatureCheck(),
	}
	flags, ok := p.Throttled(ctx)
	checks = append(checks, powerCheck(flags, ok))
	if c, ok := p.usbPowerCheck(); ok {
		checks = append(checks, c)
	}
	checks = append(checks, throttlingCheck(flags, ok), bootCheck(info.BootDevice))
	if p.ClockSynced != nil {
		synced, known := p.ClockSynced()
		checks = append(checks, clockCheck(synced, known))
	}
	return checks
}

// clockCheck: a Pi has no battery-backed clock. Without network time it
// restarts from the time of its last shutdown, and every date it writes
// (alerts, snapshots, journal) is wrong. Seen on the Pi 5 after a boot
// without network: alerts dated six days back.
func clockCheck(synced, known bool) Check {
	switch {
	case !known:
		return check("clock", StatusUnknown, "clock.unknown", nil)
	case synced:
		return check("clock", StatusOK, "clock.synced", nil)
	default:
		return check("clock", StatusWarn, "clock.not_synced", nil)
	}
}

// Raspberry Pi 5 firmware: power supply negotiated over USB-PD (mA), and
// whether the USB ports get their full 1.6 A instead of 600 mA in total.
const (
	dtMaxCurrent   = "proc/device-tree/chosen/power/max_current"
	dtUSBFullPower = "proc/device-tree/chosen/power/usb_max_current_enable"
)

// usbPowerCheck tells whether the Pi 5 firmware lifted its USB current limit.
// Without a supply that announces 5 A (the official 27 W one does), the four
// USB ports share 600 mA and bus-powered disks drop out under load: the most
// likely failure of a Pi 5 NAS. ok is false when the firmware does not expose
// the flag (Pi 4 and older, other hardware): there is nothing to report.
func (p *Prober) usbPowerCheck() (Check, bool) {
	full, ok := p.readU32(dtUSBFullPower)
	if !ok {
		return Check{}, false
	}
	params := map[string]any{}
	if mA, ok := p.readU32(dtMaxCurrent); ok {
		params["milliamps"] = mA
	}
	if full == 1 {
		return check("usbPower", StatusOK, "usbPower.full", params), true
	}
	return check("usbPower", StatusWarn, "usbPower.limited", params), true
}

func check(id string, status Status, code string, params map[string]any) Check {
	if params == nil {
		params = map[string]any{}
	}
	return Check{ID: id, Status: status, Code: code, Params: params}
}

func modelCheck(info Info) Check {
	switch {
	case info.IsRaspberryPi:
		return check("model", StatusOK, "model.raspberry_pi", map[string]any{"model": info.Model})
	case info.Model != "":
		return check("model", StatusInfo, "model.other", map[string]any{"model": info.Model})
	default:
		return check("model", StatusUnknown, "model.unknown", nil)
	}
}

func archCheck(arch string) Check {
	params := map[string]any{"arch": arch}
	switch arch {
	case "aarch64", "x86_64", "riscv64":
		return check("arch", StatusOK, "arch.64bit", params)
	case "":
		return check("arch", StatusUnknown, "arch.unknown", nil)
	default:
		return check("arch", StatusCrit, "arch.32bit", params)
	}
}

func memoryCheck(total uint64) Check {
	params := map[string]any{"bytes": total}
	switch {
	case total == 0:
		return check("memory", StatusUnknown, "memory.unknown", nil)
	case total < memVeryLowBytes:
		return check("memory", StatusCrit, "memory.very_low", params)
	case total < memLowBytes:
		return check("memory", StatusWarn, "memory.low", params)
	default:
		return check("memory", StatusOK, "memory.ok", params)
	}
}

func (p *Prober) temperatureCheck() Check {
	c, ok := p.Temperature()
	if !ok {
		return check("temperature", StatusUnknown, "temperature.unknown", nil)
	}
	params := map[string]any{"celsius": math.Round(c*10) / 10}
	switch {
	case c >= tempHotC:
		return check("temperature", StatusCrit, "temperature.hot", params)
	case c >= tempWarmC:
		return check("temperature", StatusWarn, "temperature.warm", params)
	default:
		return check("temperature", StatusOK, "temperature.ok", params)
	}
}

func powerCheck(flags uint32, ok bool) Check {
	switch {
	case !ok:
		return check("power", StatusUnknown, "power.unknown", nil)
	case flags&UnderVoltageNow != 0:
		return check("power", StatusCrit, "power.under_voltage_now", nil)
	case flags&UnderVoltageOccurred != 0:
		return check("power", StatusWarn, "power.under_voltage_occurred", nil)
	default:
		return check("power", StatusOK, "power.ok", nil)
	}
}

func throttlingCheck(flags uint32, ok bool) Check {
	switch {
	case !ok:
		return check("throttling", StatusUnknown, "throttling.unknown", nil)
	case flags&(FreqCappedNow|ThrottledNow|SoftTempLimitNow) != 0:
		return check("throttling", StatusCrit, "throttling.active", nil)
	case flags&(FreqCappedOccurred|ThrottledOccurred|SoftTempLimitOccurred) != 0:
		return check("throttling", StatusWarn, "throttling.occurred", nil)
	default:
		return check("throttling", StatusOK, "throttling.none", nil)
	}
}

func bootCheck(b BootDevice) Check {
	params := map[string]any{"device": b.Device}
	switch b.Kind {
	case "sd_card":
		return check("bootDevice", StatusWarn, "bootDevice.sd_card", params)
	case "ssd":
		return check("bootDevice", StatusOK, "bootDevice.ssd", params)
	case "other":
		return check("bootDevice", StatusInfo, "bootDevice.other", params)
	default:
		return check("bootDevice", StatusUnknown, "bootDevice.unknown", nil)
	}
}
