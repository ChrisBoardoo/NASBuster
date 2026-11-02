// SPDX-License-Identifier: AGPL-3.0-or-later

package helper

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMkdirAllInStaysInside: a link planted in an application's folder (by
// one of its containers) cannot make the helper create a folder elsewhere.
func TestMkdirAllInStaysInside(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "data")); err != nil {
		t.Fatal(err)
	}
	if err := (OSFiles{}).MkdirAllIn(root, "data/sub", 0o755); err == nil {
		t.Fatal("followed a link out of the folder")
	}
	if _, err := os.Lstat(filepath.Join(outside, "sub")); !os.IsNotExist(err) {
		t.Fatalf("created outside: %v", err)
	}
	if err := (OSFiles{}).MkdirAllIn(root, "conf/tls", 0o755); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(filepath.Join(root, "conf", "tls")); err != nil || !fi.IsDir() {
		t.Fatalf("conf/tls = %v %v", fi, err)
	}
}
