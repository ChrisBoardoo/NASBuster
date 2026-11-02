// SPDX-License-Identifier: AGPL-3.0-or-later

// Package shares models SMB shares and NAS users, renders smb.conf from a
// fixed template and reads Samba sessions (ADR-0009).
//
// The rendered configuration contains no free-form directive: every value is
// either a constant or a name that passed a strict regular expression.
package shares

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/crosswax/nasbuster/internal/storage"
)

// Access modes.
const (
	ReadWrite = "rw"
	ReadOnly  = "ro"
)

// UsersGroup is the Linux group holding every NAS user.
const UsersGroup = "cwnsb-users"

// Header marks the files CWNSB owns.
const Header = "# Managed by CrossWax NasBuster — do not edit: changes are overwritten."

var (
	// Share names: max 20 chars so "cwnsb-share-<name>" fits Linux's 32-char group limit.
	shareNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,19}$`)
	userNameRe  = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,31}$`)
)

// reservedShares are section names Samba treats specially.
var reservedShares = []string{"global", "homes", "printers", "print$", "ipc$"}

// reservedUsers can never be NAS users, even if absent from this system.
var reservedUsers = []string{
	"root", "daemon", "bin", "sys", "sync", "games", "man", "lp", "mail", "news", "uucp", "proxy",
	"www-data", "backup", "list", "irc", "nobody", "systemd-network", "systemd-resolve", "messagebus",
	"sshd", "cwnsb", "admin", "administrator", "guest", "pi",
}

// ValidShareName reports whether name can be a share (and a group suffix).
func ValidShareName(name string) bool {
	return shareNameRe.MatchString(name) && !slices.Contains(reservedShares, name)
}

// ValidUserName reports whether name can be a NAS user.
func ValidUserName(name string) bool {
	return userNameRe.MatchString(name) && !slices.Contains(reservedUsers, name)
}

// Password bounds for SMB passwords. Newlines are refused: passwords are
// piped to smbpasswd one per line.
const (
	PasswordMin = 8
	PasswordMax = 128
)

// ValidPassword reports whether pw is acceptable.
func ValidPassword(pw string) bool {
	return len(pw) >= PasswordMin && len(pw) <= PasswordMax && !strings.ContainsAny(pw, "\r\n\x00")
}

// SharePath is where a share lives: a subvolume at the root of its pool.
func SharePath(pool, name string) string { return storage.MountPoint(pool) + "/" + name }

// ShareGroup is the Linux group owning a share's files.
func ShareGroup(name string) string { return "cwnsb-share-" + name }

// Access grants one user access to a share.
type Access struct {
	User string `json:"user"`
	Mode string `json:"mode"` // rw or ro
}

// Share is the desired state of one share.
type Share struct {
	Name   string   `json:"name"`
	Pool   string   `json:"pool"`
	Access []Access `json:"access"`
	// TimeMachine makes the share a Time Machine destination for Macs
	// (Samba then advertises _adisk._tcp itself). MaxGiB 0 = no limit.
	TimeMachine       bool `json:"timeMachine"`
	TimeMachineMaxGiB int  `json:"timeMachineMaxGiB"`
	// Recycle keeps deleted files in a visible "#recycle" folder of the
	// share for RecycleDays days (what is deleted between two snapshots).
	Recycle     bool `json:"recycle"`
	RecycleDays int  `json:"recycleDays"`
}

// RecycleDir is the recycle bin folder of a share (visible: users restore
// by moving files back).
const RecycleDir = "#recycle"

// MaxTimeMachineGiB bounds the Time Machine size limit (1 PiB).
const MaxTimeMachineGiB = 1 << 20

// Validate checks a share's names and access list.
func (s Share) Validate() error {
	if !ValidShareName(s.Name) {
		return fmt.Errorf("invalid share name %q", s.Name)
	}
	if !storage.ValidPoolName(s.Pool) {
		return fmt.Errorf("invalid pool name %q", s.Pool)
	}
	if s.TimeMachineMaxGiB < 0 || s.TimeMachineMaxGiB > MaxTimeMachineGiB || (!s.TimeMachine && s.TimeMachineMaxGiB != 0) {
		return fmt.Errorf("invalid Time Machine size limit %d", s.TimeMachineMaxGiB)
	}
	if (s.Recycle && (s.RecycleDays < 1 || s.RecycleDays > 365)) || (!s.Recycle && s.RecycleDays != 0) {
		return fmt.Errorf("invalid recycle bin retention %d", s.RecycleDays)
	}
	seen := map[string]bool{}
	for _, a := range s.Access {
		if !ValidUserName(a.User) {
			return fmt.Errorf("invalid user %q", a.User)
		}
		if a.Mode != ReadWrite && a.Mode != ReadOnly {
			return fmt.Errorf("invalid access mode %q", a.Mode)
		}
		if seen[a.User] {
			return fmt.Errorf("user %q listed twice", a.User)
		}
		seen[a.User] = true
	}
	return nil
}

// ErrDuplicateShare is returned when two shares have the same name.
var ErrDuplicateShare = errors.New("duplicate share name")

// Render produces the complete smb.conf for the given shares.
func Render(shares []Share) (string, error) {
	sorted := slices.Clone(shares)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	var b strings.Builder
	b.WriteString(Header + "\n")
	b.WriteString(globalSection)
	for i, s := range sorted {
		if err := s.Validate(); err != nil {
			return "", err
		}
		if i > 0 && sorted[i-1].Name == s.Name {
			return "", ErrDuplicateShare
		}
		var valid, write []string
		for _, a := range s.Access {
			valid = append(valid, a.User)
			if a.Mode == ReadWrite {
				write = append(write, a.User)
			}
		}
		sort.Strings(valid)
		sort.Strings(write)
		fmt.Fprintf(&b, "\n[%s]\n", s.Name)
		fmt.Fprintf(&b, "   path = %s\n", SharePath(s.Pool, s.Name))
		if len(valid) == 0 {
			// Nobody may connect: an explicit, impossible requirement rather
			// than an empty list (which would mean "everyone").
			b.WriteString("   valid users = @" + UsersGroup + "\n   invalid users = @" + UsersGroup + "\n")
		} else {
			b.WriteString("   valid users = " + strings.Join(valid, " ") + "\n")
		}
		b.WriteString("   read only = yes\n")
		if len(write) > 0 {
			b.WriteString("   write list = " + strings.Join(write, " ") + "\n")
		}
		b.WriteString("   force group = " + ShareGroup(s.Name) + "\n")
		if s.TimeMachine {
			b.WriteString("   fruit:time machine = yes\n")
			if s.TimeMachineMaxGiB > 0 {
				fmt.Fprintf(&b, "   fruit:time machine max size = %dG\n", s.TimeMachineMaxGiB)
			}
		}
		if s.Recycle {
			b.WriteString("   vfs objects = catia fruit streams_xattr shadow_copy2 recycle\n" + recycleOptions)
		} else {
			b.WriteString("   vfs objects = catia fruit streams_xattr shadow_copy2\n")
		}
		b.WriteString(shareTail)
	}
	return b.String(), nil
}

// globalSection: SMB2.10+ only, no guest, no printers, no NetBIOS.
const globalSection = `[global]
   server string = CrossWax NasBuster
   server role = standalone server
   workgroup = WORKGROUP
   server min protocol = SMB2_10
   map to guest = never
   restrict anonymous = 2
   disable netbios = yes
   smb ports = 445
   load printers = no
   printing = bsd
   printcap name = /dev/null
   disable spoolss = yes
   usershare max shares = 0
   unix password sync = no
   log file = /var/log/samba/log.%m
   max log size = 1000
   logging = file
   fruit:metadata = stream
   fruit:model = RackMac
`

// shareTail is common to every share. Snapshots in .snapshots/@GMT-… show
// as Windows "Previous Versions" (verified in the lab, ADR-0010).
const shareTail = `   create mask = 0660
   force create mode = 0660
   directory mask = 2770
   force directory mode = 2770
   browseable = yes
   hide files = /.snapshots/
   shadow:snapdir = .snapshots
   shadow:format = @GMT-%Y.%m.%d-%H.%M.%S
   shadow:sort = desc
   shadow:localtime = no
`

// Session is a connected SMB client (API: GET /shares/sessions).
type Session struct {
	User      string `json:"user"`
	Machine   string `json:"machine"`
	Address   string `json:"address"`
	Dialect   string `json:"dialect"`
	Encrypted bool   `json:"encrypted"`
	Since     int64  `json:"since"` // unix seconds
}

// Connection is a client connected to one share.
type Connection struct {
	Share   string `json:"share"`
	Machine string `json:"machine"`
	Since   int64  `json:"since"` // unix seconds
}

// unixTime converts smbstatus' RFC 3339 times ("2026-10-01T23:16:30.022167+02:00").
func unixTime(v string) int64 {
	t, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		return 0
	}
	return t.Unix()
}

// recycleOptions: keep the folder tree and every version; the deletion date
// becomes the file's date (the purge counts from it).
const recycleOptions = `   recycle:repository = ` + RecycleDir + `
   recycle:keeptree = yes
   recycle:versions = yes
   recycle:touch = yes
   recycle:touch_mtime = yes
   recycle:directory_mode = 2770
   recycle:subdir_mode = 2770
   recycle:exclude = *.tmp,~$*,.DS_Store,Thumbs.db
   recycle:exclude_dir = .snapshots,` + RecycleDir + `
`

// TimeMachineShares lists the shares of an smb.conf text that are Time
// Machine destinations (to know whether mDNS announcements must change).
func TimeMachineShares(conf string) []string {
	var out []string
	section := ""
	for _, line := range strings.Split(conf, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.Trim(line, "[]")
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok && strings.TrimSpace(k) == "fruit:time machine" && strings.TrimSpace(v) == "yes" {
			out = append(out, section)
		}
	}
	sort.Strings(out)
	return out
}

// ParseSmbstatus reads `smbstatus --json` (Samba 4.16+; verified on 4.22).
func ParseSmbstatus(data []byte) ([]Session, []Connection, error) {
	var out struct {
		Sessions map[string]struct {
			Username      string `json:"username"`
			RemoteMachine string `json:"remote_machine"`
			Hostname      string `json:"hostname"`
			Dialect       string `json:"session_dialect"`
			CreationTime  string `json:"creation_time"`
			Encryption    struct {
				Cipher string `json:"cipher"`
			} `json:"encryption"`
		} `json:"sessions"`
		Tcons map[string]struct {
			Service     string `json:"service"`
			Machine     string `json:"machine"`
			ConnectedAt string `json:"connected_at"`
		} `json:"tcons"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, nil, fmt.Errorf("smbstatus: %w", err)
	}
	sessions := []Session{}
	for _, s := range out.Sessions {
		sessions = append(sessions, Session{
			User: s.Username, Machine: s.RemoteMachine, Address: strings.TrimPrefix(s.Hostname, "ipv4:"),
			Dialect: s.Dialect, Encrypted: s.Encryption.Cipher != "" && s.Encryption.Cipher != "-", Since: unixTime(s.CreationTime),
		})
	}
	conns := []Connection{}
	for _, t := range out.Tcons {
		if t.Service == "IPC$" {
			continue
		}
		conns = append(conns, Connection{Share: t.Service, Machine: t.Machine, Since: unixTime(t.ConnectedAt)})
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].Since < sessions[j].Since })
	sort.Slice(conns, func(i, j int) bool { return conns[i].Since < conns[j].Since })
	return sessions, conns, nil
}
