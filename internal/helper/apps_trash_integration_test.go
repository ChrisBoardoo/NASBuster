// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build integration && linux

package helper

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/crosswax/nasbuster/internal/apps"
	"github.com/crosswax/nasbuster/internal/storage"
)

// TestLabAppTrash: on a real Btrfs, removing an application with its data
// leaves a read-only snapshot of it in the pool's trash (ADR-0035). The purge
// deletes it once it is older than a week, and the pool stays destroyable.
func TestLabAppTrash(t *testing.T) {
	ctx := context.Background()
	l := newLabPool(t, 1)
	pool, _, err := l.create(storage.LayoutSingle, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	const name = "itapp"
	dir := apps.AppDir(pool, name)
	os.MkdirAll(apps.AppsDir(pool), 0o755)
	if res, err := l.run.Run(ctx, "btrfs", "subvolume", "create", dir); err != nil || res.ExitCode != 0 {
		t.Fatalf("subvolume: %v %s", err, res.Stderr)
	}
	os.WriteFile(filepath.Join(dir, apps.ComposeFile), []byte("services: {}\n"), 0o600)
	os.MkdirAll(filepath.Join(dir, "data"), 0o755)
	os.WriteFile(filepath.Join(dir, "data", "kuma.db"), []byte("the admin account"), 0o644)

	if _, err := l.ops.appRemove(ctx, AppRemoveArgs{Pool: pool, Name: name, DeleteData: true}); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("app folder still there: %v", err)
	}
	trash := apps.TrashDir(pool)
	if fi, err := os.Stat(trash); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("trash = %v %v", fi, err)
	}
	entries, _ := os.ReadDir(trash)
	if len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), "app-"+name+"-") {
		t.Fatalf("trash entries = %v", entries)
	}
	kept := filepath.Join(trash, entries[0].Name())
	if b, err := os.ReadFile(filepath.Join(kept, "data", "kuma.db")); err != nil || string(b) != "the admin account" {
		t.Fatalf("kept data = %q %v", b, err)
	}
	if res, _ := l.run.Run(ctx, "btrfs", "property", "get", "-ts", kept, "ro"); strings.TrimSpace(string(res.Stdout)) != "ro=true" {
		t.Fatalf("kept snapshot not read-only: %q", res.Stdout)
	}
	if err := os.WriteFile(filepath.Join(kept, "data", "x"), []byte("x"), 0o644); err == nil {
		t.Fatal("wrote into the kept snapshot")
	}

	// A day later: kept. Eight days later: purged.
	l.ops.Now = func() time.Time { return time.Now().Add(24 * time.Hour) }
	if res, err := l.ops.appTrashPurge(ctx, TrashPurgeArgs{Pool: pool}); err != nil || res["purged"] != 0 {
		t.Fatalf("purge after a day = %v %v", res, err)
	}
	l.ops.Now = func() time.Time { return time.Now().Add(8 * 24 * time.Hour) }
	if res, err := l.ops.appTrashPurge(ctx, TrashPurgeArgs{Pool: pool}); err != nil || res["purged"] != 1 {
		t.Fatalf("purge after 8 days = %v %v", res, err)
	}
	if _, err := os.Stat(kept); !os.IsNotExist(err) {
		t.Fatalf("kept snapshot still there: %v", err)
	}
	l.ops.Now = nil

	// The trash lives in .cwnsb: the pool is empty again, and can be destroyed.
	if _, err := l.ops.poolDestroy(ctx, PoolConfirmArgs{Name: pool, Confirm: pool}); err != nil {
		t.Fatalf("destroy: %v", err)
	}
}
