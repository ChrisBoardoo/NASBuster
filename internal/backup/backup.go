// SPDX-License-Identifier: AGPL-3.0-or-later

// Package backup holds the pure logic of the configuration backups kept on
// the pools (ADR-0014): names, paths, the manifest, the identities that must
// survive a reinstall (UIDs and GIDs own the files), retention.
package backup

import (
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/crosswax/nasbuster/internal/shares"
	"github.com/crosswax/nasbuster/internal/storage"
)

// Files of one backup.
const (
	DatabaseFile = "config.db"
	SambaFile    = "smbpasswd.txt"
	ManifestFile = "manifest.json"
	Keep         = 7        // backups kept per pool
	MaxDatabase  = 64 << 20 // bytes
)

// DaemonExport is where nasbusterd leaves its consistent copy for the helper.
const DaemonExport = "/var/lib/cwnsb/backup/config.db"

// Dir is the backup directory of a pool (never shared: names starting with a
// dot cannot be share names).
func Dir(pool string) string { return storage.MountPoint(pool) + "/.cwnsb/backup" }

var nameRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}Z$`)

// Name is a backup's directory name: UTC, sortable.
func Name(t time.Time) string { return t.UTC().Format("2006-01-02T15-04-05Z") }

// ValidName checks a backup name.
func ValidName(name string) bool { return nameRe.MatchString(name) }

// Time parses a backup name.
func Time(name string) (time.Time, bool) {
	t, err := time.Parse("2006-01-02T15-04-05Z", name)
	return t, err == nil
}

var installationRe = regexp.MustCompile(`^[0-9a-f]{32}$`)

// ValidInstallation checks an installation id: 16 random bytes in hex,
// drawn once per database (store.InstallationID).
func ValidInstallation(id string) bool { return installationRe.MatchString(id) }

// Entry is a backup found on a pool, with the installation that made it
// ("" for a backup older than installation ids, or unreadable).
type Entry struct {
	Name         string
	Installation string
}

// PruneFor returns the backups to delete: the oldest of those made by this
// installation, keeping its newest Keep. A backup of another installation,
// or of none known, is never deleted (P6): a fresh SD card must not rotate
// away the backups of the installation it replaces.
func PruneFor(entries []Entry, installation string) []string {
	if !ValidInstallation(installation) {
		return nil
	}
	var mine []string
	for _, e := range entries {
		if ValidName(e.Name) && e.Installation == installation {
			mine = append(mine, e.Name)
		}
	}
	sort.Strings(mine)
	if len(mine) <= Keep {
		return nil
	}
	return mine[:len(mine)-Keep]
}

// IsSQLite checks the header of a database file.
func IsSQLite(b []byte) bool { return bytes.HasPrefix(b, []byte("SQLite format 3\x00")) }

// User is a NAS account to recreate with the same IDs.
type User struct {
	Name string `json:"name"`
	UID  int    `json:"uid"`
	GID  int    `json:"gid"` // primary group
}

// Group is a group to recreate with the same GID.
type Group struct {
	Name    string   `json:"name"`
	GID     int      `json:"gid"`
	Members []string `json:"members"`
}

// ManifestVersion 2 adds the installation, the admin and the shares (P6).
const ManifestVersion = 2

// SupportedVersion tells whether a manifest can be restored: every version
// written so far. A newer one (from a later NasBuster) is refused.
func SupportedVersion(v int) bool { return v >= 1 && v <= ManifestVersion }

// Manifest describes one backup.
type Manifest struct {
	Version  int       `json:"version"`
	Created  time.Time `json:"created"`
	Hostname string    `json:"hostname"`
	Users    []User    `json:"users"`
	Groups   []Group   `json:"groups"`
	// Version 2: which installation made it (its rotation only touches its
	// own), and what a restore brings back, to choose a backup by content.
	Installation string   `json:"installation,omitempty"`
	Admin        string   `json:"admin,omitempty"`
	Shares       []string `json:"shares"` // nil only in version 1
}

// Identities extracts, from /etc/passwd and /etc/group, the NAS users
// (members of cwnsb-users), their primary groups, cwnsb-users itself and the
// share groups.
func Identities(passwd, group []byte) ([]User, []Group) {
	type grp struct {
		gid     int
		members []string
	}
	groups := map[string]grp{}
	byGID := map[int]string{}
	for _, line := range strings.Split(string(group), "\n") {
		f := strings.Split(line, ":")
		if len(f) < 4 {
			continue
		}
		gid, err := strconv.Atoi(f[2])
		if err != nil {
			continue
		}
		var members []string
		for _, m := range strings.Split(f[3], ",") {
			if m != "" {
				members = append(members, m)
			}
		}
		groups[f[0]] = grp{gid, members}
		byGID[gid] = f[0]
	}
	nas := map[string]bool{}
	for _, m := range groups[shares.UsersGroup].members {
		nas[m] = true
	}
	var users []User
	keep := map[string]bool{shares.UsersGroup: true}
	for _, line := range strings.Split(string(passwd), "\n") {
		f := strings.Split(line, ":")
		if len(f) < 4 || !nas[f[0]] {
			continue
		}
		uid, err1 := strconv.Atoi(f[2])
		gid, err2 := strconv.Atoi(f[3])
		if err1 != nil || err2 != nil {
			continue
		}
		users = append(users, User{Name: f[0], UID: uid, GID: gid})
		if g, ok := byGID[gid]; ok {
			keep[g] = true
		}
	}
	for name := range groups {
		if strings.HasPrefix(name, "cwnsb-share-") {
			keep[name] = true
		}
	}
	var out []Group
	for name := range keep {
		g, ok := groups[name]
		if !ok {
			continue
		}
		members := g.members
		if members == nil {
			members = []string{}
		}
		out = append(out, Group{Name: name, GID: g.gid, Members: members})
	}
	sort.Slice(users, func(i, j int) bool { return users[i].Name < users[j].Name })
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return users, out
}

// FilterSamba keeps the smbpasswd lines (pdbedit -L -w) of the given users.
func FilterSamba(export []byte, users []User) []byte {
	keep := map[string]bool{}
	for _, u := range users {
		keep[u.Name] = true
	}
	var out bytes.Buffer
	for _, line := range strings.Split(string(export), "\n") {
		name, _, ok := strings.Cut(line, ":")
		if ok && keep[name] {
			fmt.Fprintln(&out, line)
		}
	}
	return out.Bytes()
}
