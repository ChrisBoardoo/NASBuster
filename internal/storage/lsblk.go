// SPDX-License-Identifier: AGPL-3.0-or-later

package storage

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// LsblkArgs is the exact lsblk invocation used for discovery (util-linux >= 2.37
// for MOUNTPOINTS). Exported so demo mode and tests can register its output.
var LsblkArgs = []string{
	"-J", "-b", "-o",
	"NAME,KNAME,PATH,SIZE,TYPE,TRAN,ROTA,RM,RO,MODEL,SERIAL,WWN,VENDOR,FSTYPE,UUID,LABEL,PARTLABEL,PTTYPE,MOUNTPOINTS,PKNAME,HOTPLUG",
}

// lsblkDevice mirrors one node of `lsblk -J`. Old util-linux versions print
// numbers and booleans as strings; the flex types accept both.
type lsblkDevice struct {
	Name        string        `json:"name"`
	KName       string        `json:"kname"`
	Path        string        `json:"path"`
	Size        flexUint      `json:"size"`
	Type        string        `json:"type"`
	Tran        string        `json:"tran"`
	Rota        flexBool      `json:"rota"`
	RM          flexBool      `json:"rm"`
	RO          flexBool      `json:"ro"`
	Model       string        `json:"model"`
	Serial      string        `json:"serial"`
	WWN         string        `json:"wwn"`
	Vendor      string        `json:"vendor"`
	FSType      string        `json:"fstype"`
	UUID        string        `json:"uuid"`
	Label       string        `json:"label"`
	PartLabel   string        `json:"partlabel"`
	PTType      string        `json:"pttype"`
	Mountpoints []*string     `json:"mountpoints"`
	Mountpoint  *string       `json:"mountpoint"` // util-linux < 2.37
	PKName      string        `json:"pkname"`
	Hotplug     flexBool      `json:"hotplug"`
	Children    []lsblkDevice `json:"children"`
}

// mounts returns the non-empty mount points.
func (d lsblkDevice) mounts() []string {
	var out []string
	for _, m := range d.Mountpoints {
		if m != nil && *m != "" {
			out = append(out, *m)
		}
	}
	if d.Mountpoint != nil && *d.Mountpoint != "" && len(out) == 0 {
		out = append(out, *d.Mountpoint)
	}
	return out
}

// walk visits d and all its descendants.
func (d lsblkDevice) walk(fn func(lsblkDevice)) {
	fn(d)
	for _, c := range d.Children {
		c.walk(fn)
	}
}

func parseLsblk(data []byte) ([]lsblkDevice, error) {
	var out struct {
		BlockDevices []lsblkDevice `json:"blockdevices"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("lsblk: %w", err)
	}
	return out.BlockDevices, nil
}

type flexBool bool

func (b *flexBool) UnmarshalJSON(data []byte) error {
	switch s := strings.Trim(string(data), `"`); s {
	case "true", "1":
		*b = true
	case "false", "0", "null", "":
		*b = false
	default:
		return fmt.Errorf("invalid boolean %s", data)
	}
	return nil
}

type flexUint uint64

func (u *flexUint) UnmarshalJSON(data []byte) error {
	s := strings.Trim(string(data), `"`)
	if s == "null" || s == "" {
		*u = 0
		return nil
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid size %s", data)
	}
	*u = flexUint(v)
	return nil
}
