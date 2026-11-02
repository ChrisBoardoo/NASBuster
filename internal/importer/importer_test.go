// SPDX-License-Identifier: AGPL-3.0-or-later

package importer

import (
	"testing"
	"time"
)

// Outputs captured in the lab (rsync 3.5.0, Debian 13).
const (
	labDryRun = "Number of files: 5 (reg: 2, dir: 2, link: 1)\nTotal file size: 3,001,009 bytes\nTotal transferred file size: 3,001,000 bytes\n"
	labLog    = "2026/10/02 01:49:46 [366] building file list\n2026/10/02 01:49:46 [366] 9 link\n2026/10/02 01:49:46 [366] 1000 two.txt\n" +
		"2026/10/02 01:49:46 [366] 4096 a/\n2026/10/02 01:49:46 [366] 3000000 a/one.bin\n2026/10/02 01:49:46 [366] sent 3001968 bytes  received 74 bytes  total size 3001009\n"
)

func TestParse(t *testing.T) {
	if n, ok := ParseDryRun(labDryRun); !ok || n != 3001000 {
		t.Fatalf("dry run = %d %v", n, ok)
	}
	if _, ok := ParseDryRun("nothing"); ok {
		t.Fatal("no total")
	}
	if b, f := ParseCopyLog(labLog); b != 3001009 || f != 3 {
		t.Fatalf("log = %d bytes, %d files", b, f)
	}
}

func TestSafety(t *testing.T) {
	for _, fs := range []string{"ext4", "xfs", "exfat", "vfat", "ntfs", "btrfs"} {
		if _, ok := Supported(fs); !ok {
			t.Errorf("%s not supported", fs)
		}
	}
	if mt, _ := Supported("ntfs"); mt != "ntfs3" {
		t.Fatal(mt)
	}
	for _, fs := range []string{"swap", "linux_raid_member", "crypto_LUKS", ""} {
		if _, ok := Supported(fs); ok {
			t.Errorf("%s supported", fs)
		}
	}
	if MountOptions("ext4") != "ro,nosuid,nodev,noexec,norecovery" || MountOptions("exfat") != "ro,nosuid,nodev,noexec" {
		t.Fatal(MountOptions("ext4"))
	}
	for _, ok := range []string{"", "Photos", "Photos/2024", "a b/c"} {
		if !ValidSubdir(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"/etc", "..", "../x", "a/../../x", "a/./b", "a//b", "a/"} {
		if ValidSubdir(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
	for _, ok := range []string{"6f1c2a7e-3b4d-4c8e-9f10-2a3b4c5d6e7f", "ABCD-1234", "0123456789ABCDEF"} {
		if !ValidUUID(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"", "../x", "abc", "a b", "-abcd"} {
		if ValidUUID(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
	if got := DestName("OMV disk/../x", time.Date(2026, 10, 2, 9, 5, 0, 0, time.UTC)); got != "Import OMV disk..x 2026-10-02 0905" {
		t.Fatal(got)
	}
}
