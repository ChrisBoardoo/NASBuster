// SPDX-License-Identifier: AGPL-3.0-or-later

package storage

import (
	"encoding/json"
	"strings"
)

// SmartArgs returns the smartctl invocation for a by-id path. deviceType ""
// lets smartctl auto-detect; "sat" forces SCSI/ATA translation (USB bridges).
func SmartArgs(path, deviceType string) []string {
	args := []string{"-j", "-a"}
	if deviceType != "" {
		args = append(args, "-d", deviceType)
	}
	return append(args, path)
}

// SmartAttribute is one ATA attribute we surface.
type SmartAttribute struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Raw    int64  `json:"raw"`
	Status string `json:"status"` // ok or warn
}

// NVMeHealth is the NVMe health log subset we surface.
type NVMeHealth struct {
	PercentageUsed  int   `json:"percentageUsed"`
	MediaErrors     int64 `json:"mediaErrors"`
	CriticalWarning int   `json:"criticalWarning"`
	AvailableSpare  int   `json:"availableSpare"`
}

// SmartReport is the API view of a SMART read (GET /storage/disks/{id}/smart).
type SmartReport struct {
	Available          bool             `json:"available"`
	Reason             string           `json:"reason"`
	DeviceType         string           `json:"deviceType"`
	Passed             *bool            `json:"passed"`
	TemperatureCelsius *int             `json:"temperatureCelsius"`
	PowerOnHours       *int64           `json:"powerOnHours"`
	Attributes         []SmartAttribute `json:"attributes"`
	NVMe               *NVMeHealth      `json:"nvme"`
}

// Reasons for an unavailable report.
const (
	SmartUSBBridgeUnsupported = "usb_bridge_unsupported"
	SmartNotSupported         = "not_supported"
	SmartNotInstalled         = "not_installed"
	SmartError                = "error"
)

// criticalAttributes are the ATA attributes whose raw value should stay at 0.
// 199 (UDMA CRC errors) usually points at the cable or USB bridge, not the disk.
var criticalAttributes = map[int]bool{5: true, 187: true, 197: true, 198: true, 199: true}

type smartctlJSON struct {
	Smartctl struct {
		ExitStatus int `json:"exit_status"`
		Messages   []struct {
			String   string `json:"string"`
			Severity string `json:"severity"`
		} `json:"messages"`
	} `json:"smartctl"`
	Device struct {
		Type string `json:"type"`
	} `json:"device"`
	SmartSupport *struct {
		Available bool `json:"available"`
	} `json:"smart_support"`
	SmartStatus *struct {
		Passed bool `json:"passed"`
	} `json:"smart_status"`
	Temperature *struct {
		Current int `json:"current"`
	} `json:"temperature"`
	PowerOnTime *struct {
		Hours int64 `json:"hours"`
	} `json:"power_on_time"`
	ATASmartAttributes *struct {
		Table []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
			Raw  struct {
				Value int64 `json:"value"`
			} `json:"raw"`
		} `json:"table"`
	} `json:"ata_smart_attributes"`
	NVMeLog *struct {
		CriticalWarning int   `json:"critical_warning"`
		AvailableSpare  int   `json:"available_spare"`
		PercentageUsed  int   `json:"percentage_used"`
		MediaErrors     int64 `json:"media_errors"`
	} `json:"nvme_smart_health_information_log"`
}

// ParseSmartctl turns `smartctl -j` output into a report. retry is true when
// smartctl could not identify the device type and "-d sat" is worth a try.
func ParseSmartctl(stdout []byte) (report SmartReport, retry bool) {
	report.Attributes = []SmartAttribute{}
	var out smartctlJSON
	if err := json.Unmarshal(stdout, &out); err != nil {
		report.Reason = SmartError
		return report, false
	}

	var messages strings.Builder
	for _, m := range out.Smartctl.Messages {
		messages.WriteString(strings.ToLower(m.String))
		messages.WriteByte('\n')
	}
	msg := messages.String()
	// Exit bits 0 (command line) and 1 (device open) mean nothing was read.
	unreadable := out.Smartctl.ExitStatus&0b11 != 0
	if unreadable {
		switch {
		case strings.Contains(msg, "usb bridge"), strings.Contains(msg, "specify device type"):
			report.Reason = SmartUSBBridgeUnsupported
			return report, true
		case strings.Contains(msg, "unable to detect device type"):
			report.Reason = SmartNotSupported
			return report, false
		default:
			report.Reason = SmartError
			return report, out.Device.Type == "scsi"
		}
	}
	if out.SmartStatus == nil && out.NVMeLog == nil {
		report.Reason = SmartNotSupported
		if out.Device.Type == "scsi" {
			// A USB bridge answering as plain SCSI often hides an ATA disk.
			report.Reason = SmartUSBBridgeUnsupported
			return report, true
		}
		return report, false
	}

	report.Available = true
	report.DeviceType = out.Device.Type
	if out.SmartStatus != nil {
		passed := out.SmartStatus.Passed
		report.Passed = &passed
	}
	if out.Temperature != nil {
		t := out.Temperature.Current
		report.TemperatureCelsius = &t
	}
	if out.PowerOnTime != nil {
		h := out.PowerOnTime.Hours
		report.PowerOnHours = &h
	}
	if out.ATASmartAttributes != nil {
		for _, a := range out.ATASmartAttributes.Table {
			if !criticalAttributes[a.ID] {
				continue
			}
			status := "ok"
			if a.Raw.Value > 0 {
				status = "warn"
			}
			report.Attributes = append(report.Attributes, SmartAttribute{ID: a.ID, Name: a.Name, Raw: a.Raw.Value, Status: status})
		}
	}
	if out.NVMeLog != nil {
		report.NVMe = &NVMeHealth{
			PercentageUsed:  out.NVMeLog.PercentageUsed,
			MediaErrors:     out.NVMeLog.MediaErrors,
			CriticalWarning: out.NVMeLog.CriticalWarning,
			AvailableSpare:  out.NVMeLog.AvailableSpare,
		}
	}
	return report, false
}
