// SPDX-License-Identifier: AGPL-3.0-or-later

// Package validate holds the input rules shared by the API (nasbusterd) and the
// privileged helper (nbhelper). The helper re-validates everything it receives:
// these functions are a security boundary, keep them strict.
package validate

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	usernameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{2,31}$`)
	// Account of the machine itself (Debian's useradd rules, without the
	// trailing "$" of Samba machine accounts). Never starts with a dash.
	machineUserRe = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	// RFC 1123 label, lower-case only.
	hostnameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	// Characters udev keeps in /dev/disk/by-id names.
	diskIDRe = regexp.MustCompile(`^[0-9A-Za-z#+.:=@_-]{1,255}$`)
	// IANA zone names: up to three components, no dots.
	timezoneRe = regexp.MustCompile(`^[A-Za-z0-9_+-]{1,64}(/[A-Za-z0-9_+-]{1,64}){0,2}$`)
)

// Password length bounds. The upper bound caps hashing cost.
const (
	PasswordMin = 10
	PasswordMax = 256
)

// MachinePasswordMax is the longest machine password checked (PAM_MAX_RESP_SIZE - 1).
const MachinePasswordMax = 511

// Languages supported by the UI.
var Languages = []string{"en", "fr"}

// Username reports whether s is an acceptable account name.
func Username(s string) bool { return usernameRe.MatchString(s) }

// MachineUser reports whether s is a valid name for an account of the machine
// (the one created in Raspberry Pi Imager or by the Debian installer).
func MachineUser(s string) bool { return machineUserRe.MatchString(s) }

// MachinePassword reports whether s can be handed to the system's password
// checker: not empty, at most MachinePasswordMax bytes, and no NUL byte (the
// checker reads up to the first NUL).
func MachinePassword(s string) bool {
	return s != "" && len(s) <= MachinePasswordMax && !strings.ContainsRune(s, 0)
}

// Password reports whether s has an acceptable length (in characters).
func Password(s string) bool {
	n := utf8.RuneCountInString(s)
	return n >= PasswordMin && n <= PasswordMax && len(s) <= 4*PasswordMax
}

// Hostname reports whether s is a valid single-label hostname.
func Hostname(s string) bool { return hostnameRe.MatchString(s) }

// Timezone reports whether s looks like an IANA zone name.
// Callers must still check that the zone exists on the system.
func Timezone(s string) bool { return timezoneRe.MatchString(s) }

// DiskID reports whether s is a safe /dev/disk/by-id basename: no path
// separator, no "..", no leading dash (never mistaken for an option).
func DiskID(s string) bool {
	return diskIDRe.MatchString(s) && !strings.Contains(s, "..") && s != "." && !strings.HasPrefix(s, "-")
}

// Language reports whether s is a supported UI language.
func Language(s string) bool {
	for _, l := range Languages {
		if s == l {
			return true
		}
	}
	return false
}
