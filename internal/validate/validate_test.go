// SPDX-License-Identifier: AGPL-3.0-or-later

package validate

import (
	"strings"
	"testing"
)

func TestDiskID(t *testing.T) {
	good := []string{
		"wwn-0x5000c500c1234567",
		"ata-ST4000VN008-2DR166_ZDH1ABCD",
		"usb-JMicron_Generic_0123456789ABCDEF-0:0",
		"nvme-eui.0025385b71b0d1a2",
		"lab-loop3",
	}
	for _, s := range good {
		if !DiskID(s) {
			t.Errorf("DiskID(%q) = false, want true", s)
		}
	}
	bad := []string{"", ".", "..", "../sda", "a/b", "-x", "wwn..x", "sda;rm", "a b", `a\b`, strings.Repeat("a", 256)}
	for _, s := range bad {
		if DiskID(s) {
			t.Errorf("DiskID(%q) = true, want false", s)
		}
	}
}

func TestHostname(t *testing.T) {
	for _, s := range []string{"nasbuster", "nas-01", "a", strings.Repeat("a", 63)} {
		if !Hostname(s) {
			t.Errorf("Hostname(%q) = false", s)
		}
	}
	for _, s := range []string{"", "-nas", "nas-", "NAS", "nas.local", "nas_01", strings.Repeat("a", 64), "nas\n"} {
		if Hostname(s) {
			t.Errorf("Hostname(%q) = true", s)
		}
	}
}

func TestTimezone(t *testing.T) {
	for _, s := range []string{"UTC", "Europe/Paris", "America/Argentina/Buenos_Aires", "Etc/GMT+1", "America/Port-au-Prince"} {
		if !Timezone(s) {
			t.Errorf("Timezone(%q) = false", s)
		}
	}
	for _, s := range []string{"", "../etc/passwd", "Europe/../../etc", "/Europe/Paris", "Europe/Paris/", "a/b/c/d", "Europe Paris"} {
		if Timezone(s) {
			t.Errorf("Timezone(%q) = true", s)
		}
	}
}

func TestUsernamePasswordLanguage(t *testing.T) {
	if !Username("admin") || Username("Admin") || Username("ad") || Username("1admin") {
		t.Error("username rules")
	}
	if Password("short") || !Password("correct horse") || Password(strings.Repeat("x", 257)) {
		t.Error("password rules")
	}
	if !Language("fr") || Language("de") {
		t.Error("language rules")
	}
}

func TestMachineUser(t *testing.T) {
	for _, s := range []string{"pi", "crosswax", "root", "_svc", "a", "jean-luc", "user_2", strings.Repeat("a", 32)} {
		if !MachineUser(s) {
			t.Errorf("MachineUser(%q) = false, want true", s)
		}
	}
	// A leading dash would be read as an option by unix_chkpwd and id.
	for _, s := range []string{"", "-n", "--help", "Pi", "1pi", "pi$", "pi user", "pi/x", "pi\n", "é", strings.Repeat("a", 33)} {
		if MachineUser(s) {
			t.Errorf("MachineUser(%q) = true, want false", s)
		}
	}
}

func TestMachinePassword(t *testing.T) {
	for _, s := range []string{"x", `Tr0ub4dor & "3" é $HOME`, strings.Repeat("a", MachinePasswordMax)} {
		if !MachinePassword(s) {
			t.Errorf("MachinePassword(%q) = false, want true", s)
		}
	}
	// The checker stops at the first NUL: "right\x00anything" must never pass for "right".
	for _, s := range []string{"", "right\x00anything", strings.Repeat("a", MachinePasswordMax+1)} {
		if MachinePassword(s) {
			t.Errorf("MachinePassword(%q) = true, want false", s)
		}
	}
}
