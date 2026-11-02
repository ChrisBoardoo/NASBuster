// SPDX-License-Identifier: AGPL-3.0-or-later

// Package storage discovers disks and reads their health.
//
// Safety model: every disk gets a role. Only "available" disks may ever be
// offered for destructive operations (M2+). "system" disks hold the OS and
// "protected" disks are everything outside the lab in lab mode: both must
// never be touched, whatever the caller asks.
package storage

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/crosswax/nasbuster/internal/sysexec"
)

// Roles.
const (
	RoleSystem    = "system"
	RoleInUse     = "in_use"
	RoleAvailable = "available"
	RoleProtected = "protected"
)

// Warnings.
const (
	WarnDuplicateSerial  = "duplicate_serial"
	WarnNoStableID       = "no_stable_id"
	WarnUSBStorageDriver = "usb_storage_driver"
	// WarnUSB2Link: the disk's USB link runs at 480 Mb/s. On a Pi 5, the two
	// blue (USB 3) ports are stacked, like the two black ones: two wide sticks
	// side by side always take one of each (seen on the test Pi).
	WarnUSB2Link = "usb2_link"
)

// LabIDPrefix marks lab loop devices ("lab-loop3").
const LabIDPrefix = "lab-"

// USBInfo describes the USB bridge in front of a disk.
type USBInfo struct {
	VendorID  string `json:"vendorId"`
	ProductID string `json:"productId"`
	Driver    string `json:"driver"`    // uas or usb-storage
	SpeedMbps int    `json:"speedMbps"` // link speed: 480 (USB 2), 5000 or more (USB 3); 0 if unknown
}

// Usage is one reason a disk is not blank.
type Usage struct {
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
}

// Partition is a direct child partition.
type Partition struct {
	Name        string   `json:"name"`
	SizeBytes   uint64   `json:"sizeBytes"`
	FSType      string   `json:"fstype"`
	Label       string   `json:"label"`
	PartLabel   string   `json:"partLabel"` // GPT partition name (cwnsb-<pool> for CWNSB members)
	UUID        string   `json:"uuid"`
	Mountpoints []string `json:"mountpoints"`
}

// Disk is a whole block device (API: GET /storage/disks).
type Disk struct {
	ID         string      `json:"id"`
	ByIDPath   string      `json:"byIdPath"`
	DevPath    string      `json:"devPath"`
	Model      string      `json:"model"`
	Vendor     string      `json:"vendor"`
	Serial     string      `json:"serial"`
	WWN        string      `json:"wwn"`
	SizeBytes  uint64      `json:"sizeBytes"`
	Transport  string      `json:"transport"`
	Rotational bool        `json:"rotational"`
	Removable  bool        `json:"removable"`
	USB        *USBInfo    `json:"usb"`
	Role       string      `json:"role"`
	Usage      []Usage     `json:"usage"`
	Partitions []Partition `json:"partitions"`
	Warnings   []string    `json:"warnings"`
}

// Discoverer lists disks with lsblk and udevadm (both unprivileged).
type Discoverer struct {
	Run sysexec.Runner
	FS  fs.FS // system root, used for /sys/block/loopN/loop/backing_file
	// LabDir enables lab mode: loop devices backed by files under LabDir are the
	// only candidates; every other disk is reported as protected.
	LabDir string
	// CacheFor avoids re-running lsblk on every request. Zero disables caching.
	CacheFor time.Duration

	mu       sync.Mutex
	cached   []Disk
	cachedAt time.Time
}

// ErrNotFound means no disk has the requested id.
var ErrNotFound = errors.New("disk not found")

// systemMounts are mount points that make a disk part of the OS.
var systemMounts = map[string]bool{
	"/": true, "/boot": true, "/boot/firmware": true, "/boot/efi": true,
	"/usr": true, "/var": true, "/home": true, "[SWAP]": true,
}

// Devices that are never disks for a NAS.
var ignoredName = regexp.MustCompile(`^(zram|ram|mtdblock|nbd|sr)\d+$|^mmcblk\d+(boot\d+|rpmb)$`)

// Disks returns all disks, sorted by device path.
func (d *Discoverer) Disks(ctx context.Context) ([]Disk, error) {
	if d.CacheFor > 0 {
		d.mu.Lock()
		if d.cached != nil && time.Since(d.cachedAt) < d.CacheFor {
			out := d.cached
			d.mu.Unlock()
			return out, nil
		}
		d.mu.Unlock()
	}

	res, err := d.Run.Run(ctx, "lsblk", LsblkArgs...)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("lsblk exited with %d: %s", res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
	devices, err := parseLsblk(res.Stdout)
	if err != nil {
		return nil, err
	}

	disks := []Disk{}
	for _, dev := range devices {
		disk, ok := d.build(ctx, dev)
		if ok {
			disks = append(disks, disk)
		}
	}
	flagDuplicateIDs(disks)
	flagDuplicateSerials(disks)
	sort.Slice(disks, func(i, j int) bool { return disks[i].DevPath < disks[j].DevPath })

	if d.CacheFor > 0 {
		d.mu.Lock()
		d.cached, d.cachedAt = disks, time.Now()
		d.mu.Unlock()
	}
	return disks, nil
}

// Invalidate drops the cache (call after any change to the disks).
func (d *Discoverer) Invalidate() {
	d.mu.Lock()
	d.cached = nil
	d.mu.Unlock()
}

// Disk returns the disk with this id. A miss bypasses the cache once: a disk
// plugged in a second ago must be found.
func (d *Discoverer) Disk(ctx context.Context, id string) (Disk, error) {
	for attempt := 0; attempt < 2; attempt++ {
		disks, err := d.Disks(ctx)
		if err != nil {
			return Disk{}, err
		}
		for _, disk := range disks {
			if disk.ID == id {
				return disk, nil
			}
		}
		if d.CacheFor == 0 {
			break
		}
		d.Invalidate()
	}
	return Disk{}, ErrNotFound
}

func (d *Discoverer) build(ctx context.Context, dev lsblkDevice) (Disk, bool) {
	name := dev.KName
	if name == "" {
		name = dev.Name
	}
	isLoop := dev.Type == "loop"
	switch {
	case dev.Type != "disk" && !isLoop:
		return Disk{}, false
	case isLoop && d.LabDir == "":
		return Disk{}, false
	case ignoredName.MatchString(name), uint64(dev.Size) == 0:
		return Disk{}, false
	}

	devPath := dev.Path
	if devPath == "" {
		devPath = "/dev/" + name
	}
	disk := Disk{
		DevPath:    devPath,
		Model:      strings.TrimSpace(dev.Model),
		Vendor:     strings.TrimSpace(dev.Vendor),
		Serial:     strings.TrimSpace(dev.Serial),
		WWN:        strings.TrimSpace(dev.WWN),
		SizeBytes:  uint64(dev.Size),
		Rotational: bool(dev.Rota),
		Removable:  bool(dev.RM),
		Usage:      []Usage{},
		Partitions: []Partition{},
		Warnings:   []string{},
	}

	var props udevProps
	if isLoop {
		disk.ID = LabIDPrefix + name
		disk.Transport = "loop"
	} else {
		props = d.udev(ctx, devPath)
		disk.ID = bestID(props.byIDLinks())
		if disk.ID != "" {
			disk.ByIDPath = "/dev/disk/by-id/" + disk.ID
		}
		disk.Transport = transport(dev, props)
		if disk.Transport == "usb" {
			vendor, product := props.usbIDs()
			disk.USB = &USBInfo{VendorID: vendor, ProductID: product, Driver: props["ID_USB_DRIVER"], SpeedMbps: d.usbSpeed(props["DEVPATH"])}
			if disk.USB.Driver == "usb-storage" {
				disk.Warnings = append(disk.Warnings, WarnUSBStorageDriver)
			}
			if disk.USB.SpeedMbps == 480 {
				disk.Warnings = append(disk.Warnings, WarnUSB2Link)
			}
		}
		if disk.WWN == "" {
			disk.WWN = props["ID_WWN"]
		}
		if disk.Model == "" {
			// SD cards have no SCSI model: udev knows their name (ID_NAME=SN32G).
			disk.Model = firstNonEmpty(strings.ReplaceAll(props["ID_MODEL"], "_", " "), props["ID_NAME"])
		}
		if disk.WWN == "" && disk.Serial == "" {
			disk.Warnings = append(disk.Warnings, WarnNoStableID)
		}
	}

	for _, c := range dev.Children {
		if c.Type == "part" {
			disk.Partitions = append(disk.Partitions, Partition{
				Name: c.Name, SizeBytes: uint64(c.Size), FSType: c.FSType,
				Label: c.Label, PartLabel: c.PartLabel, UUID: c.UUID, Mountpoints: nonNil(c.mounts()),
			})
		}
	}

	disk.Usage = usageOf(dev)
	disk.Role = RoleAvailable
	switch {
	case d.LabDir != "" && !d.isLabLoop(name, isLoop):
		disk.Role = RoleProtected
	case holdsSystem(dev):
		disk.Role = RoleSystem
	case len(disk.Usage) > 0:
		disk.Role = RoleInUse
	case bool(dev.RO):
		disk.Role = RoleInUse
		disk.Usage = append(disk.Usage, Usage{Kind: "read_only", Detail: name})
	}
	if disk.ID == "" {
		// Without a stable id a disk cannot be addressed safely: never offer it.
		disk.ID = "dev-" + name
		if disk.Role == RoleAvailable {
			disk.Role = RoleInUse
			disk.Usage = append(disk.Usage, Usage{Kind: "no_stable_id", Detail: name})
		}
	}
	return disk, true
}

func (d *Discoverer) udev(ctx context.Context, devPath string) udevProps {
	res, err := d.Run.Run(ctx, "udevadm", UdevArgs(devPath)...)
	if err != nil || res.ExitCode != 0 {
		return udevProps{}
	}
	return parseUdev(res.Stdout)
}

// usbDeviceRe matches a USB device in a sysfs path ("4-1.2"), not one of its
// interfaces ("4-1.2:1.0") nor a root hub ("usb4").
var usbDeviceRe = regexp.MustCompile(`^\d+-[\d.]+$`)

// usbSpeed reads the link speed (Mb/s) of the USB device holding a disk, from
// the disk's sysfs path (udev's DEVPATH). 0 if unknown.
func (d *Discoverer) usbSpeed(devpath string) int {
	if d.FS == nil {
		return 0
	}
	parts := strings.Split(strings.Trim(devpath, "/"), "/")
	for i := len(parts) - 1; i >= 0; i-- {
		if !usbDeviceRe.MatchString(parts[i]) {
			continue
		}
		b, err := fs.ReadFile(d.FS, "sys/"+strings.Join(parts[:i+1], "/")+"/speed")
		if err != nil {
			return 0
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 64) // "1.5", "480", "5000"
		if err != nil {
			return 0
		}
		return int(v)
	}
	return 0
}

// isLabLoop reports whether a loop device is backed by a file under LabDir.
func (d *Discoverer) isLabLoop(name string, isLoop bool) bool {
	if !isLoop || d.FS == nil {
		return false
	}
	b, err := fs.ReadFile(d.FS, "sys/block/"+name+"/loop/backing_file")
	if err != nil {
		return false
	}
	backing := path.Clean(strings.TrimSpace(string(b)))
	lab := path.Clean(d.LabDir)
	return strings.HasPrefix(backing, lab+"/")
}

func transport(dev lsblkDevice, props udevProps) string {
	name := dev.KName
	if name == "" {
		name = dev.Name
	}
	switch {
	case dev.Tran == "usb" || props["ID_BUS"] == "usb":
		return "usb"
	case dev.Tran == "nvme" || strings.HasPrefix(name, "nvme"):
		return "nvme"
	case dev.Tran == "sata" || dev.Tran == "ata" || props["ID_BUS"] == "ata":
		return "sata"
	case dev.Tran == "mmc" || strings.HasPrefix(name, "mmcblk"):
		return "sd_card"
	case strings.HasPrefix(name, "vd"), strings.TrimSpace(dev.Vendor) == "Msft", props["ID_VENDOR"] == "Msft":
		return "virtual"
	default:
		return "unknown"
	}
}

func holdsSystem(dev lsblkDevice) bool {
	found := false
	dev.walk(func(n lsblkDevice) {
		for _, m := range n.mounts() {
			if systemMounts[m] {
				found = true
			}
		}
	})
	return found
}

func usageOf(dev lsblkDevice) []Usage {
	usage := []Usage{}
	seen := map[Usage]bool{}
	add := func(u Usage) {
		if !seen[u] {
			seen[u] = true
			usage = append(usage, u)
		}
	}
	if dev.PTType != "" {
		add(Usage{Kind: "partition_table", Detail: dev.PTType})
	}
	dev.walk(func(n lsblkDevice) {
		switch n.FSType {
		case "":
		case "linux_raid_member":
			add(Usage{Kind: "md_member", Detail: n.Name})
		case "LVM2_member":
			add(Usage{Kind: "lvm_member", Detail: n.Name})
		case "crypto_LUKS":
			add(Usage{Kind: "crypto", Detail: n.Name})
		case "swap":
			add(Usage{Kind: "swap", Detail: n.Name})
		case "btrfs":
			add(Usage{Kind: "btrfs_member", Detail: firstNonEmpty(n.Label, n.UUID)})
		default:
			add(Usage{Kind: "filesystem", Detail: n.FSType + " " + firstNonEmpty(n.Label, n.Name)})
		}
		for _, m := range n.mounts() {
			add(Usage{Kind: "mounted", Detail: m})
		}
	})
	return usage
}

// flagDuplicateIDs fails closed on disks that share a by-id name. Identical
// USB sticks or bridges can report the same serial: udev then gives them the
// same /dev/disk/by-id link, which points at only one of them, so an operation
// meant for one could hit the other. Neither is addressable by that name: both
// lose their stable path (the helper refuses any write without one) and a
// blank one is no longer offered.
func flagDuplicateIDs(disks []Disk) {
	count := map[string]int{}
	for _, d := range disks {
		if d.ByIDPath != "" {
			count[d.ID]++
		}
	}
	for i := range disks {
		d := &disks[i]
		if d.ByIDPath == "" || count[d.ID] < 2 {
			continue
		}
		d.Usage = append(d.Usage, Usage{Kind: "duplicate_id", Detail: d.ID})
		d.ID, d.ByIDPath = "dev-"+strings.TrimPrefix(d.DevPath, "/dev/"), ""
		if d.Role == RoleAvailable {
			d.Role = RoleInUse
		}
	}
}

func flagDuplicateSerials(disks []Disk) {
	count := map[string]int{}
	for _, d := range disks {
		if d.Serial != "" {
			count[d.Serial]++
		}
	}
	for i := range disks {
		if s := disks[i].Serial; s != "" && count[s] > 1 {
			disks[i].Warnings = append(disks[i].Warnings, WarnDuplicateSerial)
		}
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
