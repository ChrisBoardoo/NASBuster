// SPDX-License-Identifier: AGPL-3.0-or-later

package scheduler

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/crosswax/nasbuster/internal/backup"
	"github.com/crosswax/nasbuster/internal/helper"
	"github.com/crosswax/nasbuster/internal/store"
)

// P6: on a fresh SD card, the pools just imported got a backup with nobody
// in it at once. The scheduler waits until there is something to save, or
// until the onboarding is over.
func TestNoEmptyBackupBeforeTheOnboardingEnds(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.st.DeleteShare(ctx, "photos"); err != nil {
		t.Fatal(err)
	}
	export := filepath.Join(t.TempDir(), "config.db")
	f.sched.ExportPath = export
	f.hc.(*helper.LocalClient).Ops.ReadDaemonExport = func() ([]byte, error) { return os.ReadFile(export) }

	if r := f.sched.Tick(ctx); r.Backups != 0 || len(r.Errors) != 0 {
		t.Fatalf("empty configuration backed up: %+v", r)
	}
	if _, err := f.st.CreateShare(ctx, store.Share{Name: "photos", Pool: "tank"}); err != nil {
		t.Fatal(err)
	}
	f.clock = f.clock.Add(time.Hour)
	if r := f.sched.Tick(ctx); r.Backups != 1 || len(r.Errors) != 0 {
		t.Fatalf("a share to save, no backup: %+v", r)
	}

	// The manifest says who made it and what it holds.
	id, _ := f.st.InstallationID(ctx)
	var list helper.BackupList
	if err := f.hc.Call(ctx, helper.OpConfigBackups, nil, &list); err != nil || len(list.Backups) != 1 {
		t.Fatalf("list = %+v %v", list, err)
	}
	b := list.Backups[0]
	if b.Installation != id || !backup.ValidInstallation(id) || b.Shares != 1 {
		t.Fatalf("backup = %+v, installation %s", b, id)
	}

	// Once the onboarding is over, even an empty configuration is saved.
	if err := f.st.DeleteShare(ctx, "photos"); err != nil {
		t.Fatal(err)
	}
	var onboarding json.RawMessage = []byte(`{"completed":true}`)
	if err := f.st.SetSetting(ctx, "onboarding", onboarding); err != nil {
		t.Fatal(err)
	}
	f.clock = f.clock.Add(25 * time.Hour)
	if r := f.sched.Tick(ctx); r.Backups != 1 {
		t.Fatalf("onboarding over, no backup: %+v", r)
	}
}
