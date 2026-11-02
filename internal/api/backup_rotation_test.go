// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/crosswax/nasbuster/internal/backup"
	"github.com/crosswax/nasbuster/internal/helper"
)

// P6, the dead SD card: the pool comes back with the backups of the old
// installation. The new one backs up every day and must never rotate them
// away; the list says which installation made each backup, and what it holds.
func TestBackupsOfAnotherInstallationAreKept(t *testing.T) {
	e := newEnv(t)
	e.setup()
	start := time.Now()
	e.sys.Clock = func() time.Time { return start }
	e.createPool("tank", "mirror", demoSDA, demoSDD)

	const old = "fedcba9876543210fedcba9876543210"
	base := backup.Dir("tank") + "/"
	write := func(name string, m backup.Manifest) {
		raw, _ := json.Marshal(m)
		_ = e.sys.Files.MkdirAll(base+name, 0o700)
		_ = e.sys.Files.WriteFile(base+name+"/"+backup.ManifestFile, raw, 0o600)
		_ = e.sys.Files.WriteFile(base+name+"/"+backup.DatabaseFile, []byte("SQLite format 3\x00"), 0o600)
	}
	// What the old installation left on the pool: a week of backups, and one
	// from before installation ids.
	for d := 1; d <= 7; d++ {
		write(backup.Name(time.Date(2026, 9, d, 1, 0, 0, 0, time.UTC)), backup.Manifest{Version: 2, Hostname: "rpi5-nas",
			Installation: old, Admin: "chris", Shares: []string{"lanparty"}, Users: []backup.User{{Name: "alice", UID: 1001, GID: 1001}}})
	}
	write(backup.Name(time.Date(2026, 8, 1, 1, 0, 0, 0, time.UTC)), backup.Manifest{Version: 1, Hostname: "rpi5-nas",
		Users: []backup.User{{Name: "alice", UID: 1001, GID: 1001}}})

	// Ten days of the new installation.
	for i := 0; i < 10; i++ {
		e.advance(24 * time.Hour)
		e.do("POST", "/api/v1/system/config-backup", nil, http.StatusCreated, nil)
	}

	var list helper.BackupList
	e.do("GET", "/api/v1/system/config-backups", nil, http.StatusOK, &list)
	mine, theirs, legacy := 0, 0, 0
	for _, b := range list.Backups {
		switch {
		case b.ThisInstallation:
			mine++
			if b.Admin != "admin" || b.Shares != 0 || !backup.ValidInstallation(b.Installation) {
				t.Errorf("this installation's backup = %+v", b)
			}
		case b.Installation == old:
			theirs++
			if b.Admin != "chris" || b.Shares != 1 || b.Users != 1 {
				t.Errorf("old installation's backup = %+v", b)
			}
		case b.Installation == "":
			legacy++
			if b.Shares != -1 {
				t.Errorf("legacy backup knows its shares: %+v", b)
			}
		}
	}
	if mine != 7 || theirs != 7 || legacy != 1 {
		t.Fatalf("kept %d of this installation, %d of the old one, %d older (want 7, 7, 1)", mine, theirs, legacy)
	}
}
