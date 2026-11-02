// SPDX-License-Identifier: AGPL-3.0-or-later

package policy

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// userRoot matches a User=root line in a unit file.
var userRoot = regexp.MustCompile(`(?m)^\s*User\s*=\s*(?:root|0)\s*$`)

// TestRootUnitsDoNotSetUserRoot: with User=root, NoNewPrivileges= and a
// system-call filter, systemd 257 removes CAP_SETUID from the service. The
// helper then could not run apt-get (it switches to its _apt user), and Docker
// could not be uninstalled (Pi 5, 2026-10-30). Root is the default anyway.
func TestRootUnitsDoNotSetUserRoot(t *testing.T) {
	root := repoRoot(t)
	units, err := filepath.Glob(filepath.Join(root, "packaging", "systemd", "*.service"))
	if err != nil {
		t.Fatal(err)
	}
	lab, _ := filepath.Glob(filepath.Join(root, "tools", "lab", "*.conf"))
	units = append(units, lab...)
	if len(units) == 0 {
		t.Fatal("no unit found under packaging/systemd")
	}
	for _, p := range units {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if userRoot.Match(b) {
			rel, _ := filepath.Rel(root, p)
			t.Errorf("%s sets User=root: leave it out (root is the default)", rel)
		}
	}
}

func TestUserRootPattern(t *testing.T) {
	for _, bad := range []string{"User=root", "User = root\n", "[Service]\nUser=0\n"} {
		if !userRoot.MatchString(bad) {
			t.Errorf("pattern misses %q", bad)
		}
	}
	for _, ok := range []string{"User=cwnsb", "# User=root is not set", "SocketUser=root", "User=rooted"} {
		if userRoot.MatchString(ok) {
			t.Errorf("false positive on %q", ok)
		}
	}
}
