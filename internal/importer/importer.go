// SPDX-License-Identifier: AGPL-3.0-or-later

// Package importer holds the pure logic of copying data from a foreign disk
// (an OMV disk, a USB key) into a share (ADR-0015): which filesystems, how
// to mount them read-only, where the copy goes, and rsync's outputs.
package importer

import (
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Root is where foreign filesystems are mounted (tmpfs, outside any pool).
const Root = "/run/cwnsb/import"

var uuidRe = regexp.MustCompile(`^[0-9A-Fa-f][0-9A-Fa-f-]{3,35}$`)

// ValidUUID checks a filesystem UUID (ext4 36 chars, vfat/exfat "ABCD-1234", ntfs 16 hex).
func ValidUUID(u string) bool { return uuidRe.MatchString(u) && !strings.Contains(u, "--") }

// Supported reports whether a filesystem type can be imported, and the type
// to give mount.
func Supported(fstype string) (mountType string, ok bool) {
	switch fstype {
	case "ext2", "ext3", "ext4", "xfs", "btrfs", "exfat", "vfat":
		return fstype, true
	case "ntfs", "ntfs3":
		return "ntfs3", true
	}
	return "", false
}

// MountOptions are read-only and inert; journaled filesystems are not
// replayed (that would write to a disk we promised not to modify).
func MountOptions(fstype string) string {
	opts := "ro,nosuid,nodev,noexec"
	switch fstype {
	case "ext3", "ext4", "xfs":
		opts += ",norecovery"
	case "btrfs":
		opts += ",rescue=nologreplay"
	}
	return opts
}

// MountPoint is where a source is mounted.
func MountPoint(uuid string) string { return Root + "/" + uuid }

// LogPath is the per-file log of the copy into a share.
func LogPath(share string) string { return Root + "/" + share + ".log" }

// Job names the copy job of a share.
func Job(share string) string { return "import-" + share }

var labelRe = regexp.MustCompile(`[^A-Za-z0-9 _.-]+`)

// DestName is the new folder receiving the copy, never an existing one.
func DestName(label string, t time.Time) string {
	label = strings.TrimSpace(labelRe.ReplaceAllString(label, ""))
	if len(label) > 40 {
		label = label[:40]
	}
	if label == "" {
		label = "disk"
	}
	return "Import " + label + " " + t.Format("2006-01-02 1504")
}

// ValidSubdir checks a folder inside the source chosen by the user.
func ValidSubdir(s string) bool {
	if s == "" {
		return true
	}
	if len(s) > 1024 || strings.HasPrefix(s, "/") || strings.ContainsAny(s, "\x00\n") {
		return false
	}
	return path.Clean(s) == s && s != "." && !strings.HasPrefix(s, "../") && s != ".."
}

var dryRunRe = regexp.MustCompile(`Total transferred file size: ([\d,]+) bytes`)

// ParseDryRun reads the total to copy from `rsync --dry-run --stats`.
func ParseDryRun(text string) (int64, bool) {
	m := dryRunRe.FindStringSubmatch(text)
	if m == nil {
		return 0, false
	}
	n, err := strconv.ParseInt(strings.ReplaceAll(m[1], ",", ""), 10, 64)
	return n, err == nil
}

// ParseCopyLog sums what rsync logged with --log-file-format="%l %n":
// "2026/10/02 01:49:46 [366] 1000 two.txt". Directories end with "/".
func ParseCopyLog(text string) (bytes int64, files int) {
	for _, line := range strings.Split(text, "\n") {
		_, rest, ok := strings.Cut(line, "] ")
		if !ok {
			continue
		}
		size, name, ok := strings.Cut(rest, " ")
		if !ok || strings.HasSuffix(name, "/") {
			continue
		}
		n, err := strconv.ParseInt(size, 10, 64)
		if err != nil {
			continue // "building file list", "sent … bytes  received …"
		}
		bytes += n
		files++
	}
	return bytes, files
}
