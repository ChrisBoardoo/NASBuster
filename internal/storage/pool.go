// SPDX-License-Identifier: AGPL-3.0-or-later

package storage

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
)

// Pool layouts (ADR-0008).
const (
	LayoutSingle  = "single"
	LayoutMirror  = "mirror"
	LayoutRAID10  = "raid10"
	LayoutParity1 = "parity1"
	LayoutParity2 = "parity2"
)

// Partition geometry: 1 MiB alignment at the start, 100 MiB left free at the
// end so a slightly smaller replacement disk still fits.
const (
	PartStartReserve = 1 << 20
	PartEndReserve   = 100 << 20
	gptBackupBytes   = 33 * 512 // backup GPT: header + 32 entry sectors (measured in the lab)
)

// MountRoot is where pools are mounted (/srv/cwnsb/<name>).
const MountRoot = "/srv/cwnsb"

var poolNameRe = regexp.MustCompile(`^[a-z][a-z0-9]{1,23}$`)

// ValidPoolName reports whether name is an acceptable pool name. No dash:
// it would need escaping in systemd unit names.
func ValidPoolName(name string) bool { return poolNameRe.MatchString(name) }

// Layout describes how a pool is built.
type Layout struct {
	Name        string
	MinDisks    int
	MaxDisks    int // 0 = unlimited
	Tolerates   int // disks that can fail without data loss
	SelfHealing bool
	MDLevel     int // 0 = no md array; 5 or 6
}

var layouts = []Layout{
	{Name: LayoutSingle, MinDisks: 1, MaxDisks: 1, Tolerates: 0, SelfHealing: false},
	{Name: LayoutMirror, MinDisks: 2, Tolerates: 1, SelfHealing: true},
	{Name: LayoutRAID10, MinDisks: 4, Tolerates: 1, SelfHealing: true},
	{Name: LayoutParity1, MinDisks: 3, Tolerates: 1, MDLevel: 5},
	{Name: LayoutParity2, MinDisks: 4, Tolerates: 2, MDLevel: 6},
}

// Layouts returns all layouts, in display order.
func Layouts() []Layout { return slices.Clone(layouts) }

// LayoutByName finds a layout.
func LayoutByName(name string) (Layout, bool) {
	for _, l := range layouts {
		if l.Name == name {
			return l, true
		}
	}
	return Layout{}, false
}

// BtrfsProfiles returns the mkfs.btrfs data and metadata profiles for n members.
// For md layouts the filesystem sits on a single md device.
func (l Layout) BtrfsProfiles(n int) (data, metadata string) {
	switch l.Name {
	case LayoutMirror:
		if n >= 3 {
			return "raid1", "raid1c3"
		}
		return "raid1", "raid1"
	case LayoutRAID10:
		return "raid10", "raid1c3"
	default: // single and md-backed layouts
		return "single", "dup"
	}
}

// PartitionType is the GPT type code for members (sgdisk).
func (l Layout) PartitionType() string {
	if l.MDLevel > 0 {
		return "FD00" // Linux RAID
	}
	return "8300" // Linux filesystem
}

// Plan is a capacity preview (API: POST /storage/pools/plan).
type Plan struct {
	Layout      string   `json:"layout"`
	Disks       int      `json:"disks"`
	RawBytes    uint64   `json:"rawBytes"`
	UsableBytes uint64   `json:"usableBytes"`
	Tolerates   int      `json:"tolerates"`
	SelfHealing bool     `json:"selfHealing"`
	Warnings    []string `json:"warnings"`
}

// Plan warnings.
const (
	PlanNoRedundancy = "no_redundancy"
	PlanMixedSizes   = "mixed_sizes"
	PlanDetectOnly   = "parity_detects_only"
)

// Errors returned by PlanPool.
var (
	ErrUnknownLayout = errors.New("unknown layout")
	ErrDiskCount     = errors.New("wrong number of disks for this layout")
	ErrDiskTooSmall  = errors.New("disk too small")
)

// MinDiskBytes is the smallest member we accept (lab images are 256 MiB+).
const MinDiskBytes = 256 << 20

// PartitionBytes is the usable size of the single partition on a disk.
func PartitionBytes(diskBytes uint64) uint64 {
	overhead := uint64(PartStartReserve + PartEndReserve + gptBackupBytes)
	if diskBytes <= overhead {
		return 0
	}
	return diskBytes - overhead
}

// PlanPool computes the usable capacity of a layout over disks of the given
// sizes. Filesystem overhead is ignored: figures are estimates, shown as such.
func PlanPool(layoutName string, diskBytes []uint64) (Plan, error) {
	l, ok := LayoutByName(layoutName)
	if !ok {
		return Plan{}, ErrUnknownLayout
	}
	n := len(diskBytes)
	if n < l.MinDisks || (l.MaxDisks > 0 && n > l.MaxDisks) {
		return Plan{}, fmt.Errorf("%w: %s needs %d", ErrDiskCount, l.Name, l.MinDisks)
	}
	parts := make([]uint64, n)
	var total, smallest, largest uint64
	for i, b := range diskBytes {
		if b < MinDiskBytes {
			return Plan{}, ErrDiskTooSmall
		}
		parts[i] = PartitionBytes(b)
		total += parts[i]
		if i == 0 || parts[i] < smallest {
			smallest = parts[i]
		}
		largest = max(largest, parts[i])
	}

	p := Plan{Layout: l.Name, Disks: n, RawBytes: total, Tolerates: l.Tolerates, SelfHealing: l.SelfHealing, Warnings: []string{}}
	switch l.Name {
	case LayoutSingle:
		p.UsableBytes = total
		p.Warnings = append(p.Warnings, PlanNoRedundancy)
	case LayoutMirror:
		// Btrfs RAID1 keeps 2 copies on 2 different disks.
		p.UsableBytes = min(total/2, total-largest)
	case LayoutRAID10:
		p.UsableBytes = uint64(n/2) * smallest
	case LayoutParity1:
		p.UsableBytes = uint64(n-1) * smallest
	case LayoutParity2:
		p.UsableBytes = uint64(n-2) * smallest
	}
	if l.MDLevel > 0 {
		p.Warnings = append(p.Warnings, PlanDetectOnly)
	}
	if smallest != largest && l.Name != LayoutSingle {
		p.Warnings = append(p.Warnings, PlanMixedSizes)
	}
	return p, nil
}

// MountPoint returns where the pool is mounted.
func MountPoint(pool string) string { return MountRoot + "/" + pool }

// MountUnitName returns the systemd mount unit for the pool. Valid pool names
// contain only [a-z0-9], so no systemd escaping is needed.
func MountUnitName(pool string) string { return "srv-cwnsb-" + pool + ".mount" }

// MDDevice returns the md array path of a parity pool.
func MDDevice(pool string) string { return "/dev/md/cwnsb-" + pool }

// FSLabel is the Btrfs label of the pool.
func FSLabel(pool string) string { return "cwnsb-" + pool }

// UnitMarker opens every unit the helper writes: only those are ever rewritten.
const UnitMarker = "# Managed by CrossWax NasBuster"

// MountUnit renders the systemd unit mounting the pool by filesystem UUID.
// A missing USB disk must never hold the boot (measured on the Pi 5): nofail
// keeps local-fs.target from waiting, and without the default dependencies
// multi-user.target is not ordered after the mount either (it waited 90 s:
// x-systemd.device-timeout is only read in /etc/fstab). The default
// dependencies that matter are restated: unmounted before umount.target.
func MountUnit(pool, fsUUID string) string {
	return fmt.Sprintf(UnitMarker+` — do not edit.
[Unit]
Description=CrossWax NasBuster pool %[1]s
DefaultDependencies=no
After=local-fs-pre.target
Conflicts=umount.target
Before=umount.target

[Mount]
What=/dev/disk/by-uuid/%[2]s
Where=%[3]s
Type=btrfs
Options=noatime,compress=zstd:1,nofail

[Install]
WantedBy=multi-user.target
`, pool, fsUUID, MountPoint(pool))
}
