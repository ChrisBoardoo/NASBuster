// SPDX-License-Identifier: AGPL-3.0-or-later

package storage

import (
	"path"
	"strings"
)

// UdevArgs returns the udevadm invocation for one device (unprivileged).
func UdevArgs(devPath string) []string {
	return []string{"info", "--query=property", "--name=" + devPath}
}

// udevProps holds the KEY=VALUE lines printed by `udevadm info --query=property`.
type udevProps map[string]string

func parseUdev(data []byte) udevProps {
	props := udevProps{}
	for _, line := range strings.Split(string(data), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && k != "" {
			props[k] = v
		}
	}
	return props
}

// byIDLinks returns the basenames of the /dev/disk/by-id/ links.
func (p udevProps) byIDLinks() []string {
	var ids []string
	for _, link := range strings.Fields(p["DEVLINKS"]) {
		if strings.HasPrefix(link, "/dev/disk/by-id/") {
			ids = append(ids, path.Base(link))
		}
	}
	return ids
}

// usbIDs returns the USB bridge vendor/product IDs. Recent systemd sets
// ID_USB_*; older versions only set ID_VENDOR_ID/ID_MODEL_ID for USB devices.
func (p udevProps) usbIDs() (vendor, product string) {
	vendor, product = p["ID_USB_VENDOR_ID"], p["ID_USB_MODEL_ID"]
	if vendor == "" && p["ID_BUS"] == "usb" {
		vendor, product = p["ID_VENDOR_ID"], p["ID_MODEL_ID"]
	}
	return vendor, product
}

// stableIDPrefixes ranks by-id links from most to least stable.
// wwn- comes from the drive itself; usb- often embeds the bridge serial.
var stableIDPrefixes = []string{"wwn-", "nvme-eui.", "ata-", "nvme-", "mmc-", "usb-", "scsi-"}

// bestID picks the most stable by-id link, "" if there is none.
func bestID(ids []string) string {
	for _, prefix := range stableIDPrefixes {
		best := ""
		for _, id := range ids {
			// Skip partition links and the legacy scsi-0/1 aliases.
			if !strings.HasPrefix(id, prefix) || strings.Contains(id, "-part") {
				continue
			}
			if best == "" || len(id) < len(best) || (len(id) == len(best) && id < best) {
				best = id
			}
		}
		if best != "" {
			return best
		}
	}
	return ""
}
