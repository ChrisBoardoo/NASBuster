// SPDX-License-Identifier: AGPL-3.0-or-later

package helper

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/crosswax/nasbuster/internal/storage"
	"github.com/crosswax/nasbuster/internal/sysexec"
)

// The unit written before 2026-10-24: x-systemd.device-timeout (ignored outside
// fstab) and default dependencies, which made multi-user.target wait 90 s for a
// missing USB disk on the Pi 5.
const oldTankUnit = `# Managed by CrossWax NasBuster — do not edit.
[Unit]
Description=CrossWax NasBuster pool tank

[Mount]
What=/dev/disk/by-uuid/` + testUUID + `
Where=/srv/cwnsb/tank
Type=btrfs
Options=noatime,compress=zstd:1,nofail,x-systemd.device-timeout=60s

[Install]
WantedBy=multi-user.target
`

func TestPoolUnitsRefresh(t *testing.T) {
	run := sysexec.NewFake().Set(sysexec.Result{}, "systemctl", "daemon-reload")
	files := NewMemFiles()
	dir := "/etc/systemd/system"
	files.MkdirAll(dir, 0o755)
	files.WriteFile(dir+"/srv-cwnsb-tank.mount", []byte(oldTankUnit), 0o644)
	current := storage.MountUnit("e1", "40e5468d-eafc-4524-9adf-6ddc2882d17d")
	files.WriteFile(dir+"/srv-cwnsb-e1.mount", []byte(current), 0o644)
	foreign := "[Mount]\nWhat=/dev/sdz1\nWhere=/srv/cwnsb/mine\n"
	files.WriteFile(dir+"/srv-cwnsb-mine.mount", []byte(foreign), 0o644)
	o := &Ops{Run: run, Files: files, UnitDir: dir, Logger: quietLogger(), Sleep: func(time.Duration) {}}

	res, err := o.poolUnitsRefresh(context.Background())
	if err != nil || !slices.Equal(res.Updated, []string{"tank"}) {
		t.Fatalf("refresh = %+v %v", res, err)
	}
	if b, _ := files.ReadFile(dir + "/srv-cwnsb-tank.mount"); string(b) != storage.MountUnit("tank", testUUID) {
		t.Errorf("tank unit = %s", b)
	}
	if b, _ := files.ReadFile(dir + "/srv-cwnsb-mine.mount"); string(b) != foreign {
		t.Error("a unit without our marker was rewritten")
	}
	reloads := func() int {
		n := 0
		for _, c := range run.Calls() {
			if strings.Join(c, " ") == "systemctl daemon-reload" {
				n++
			}
		}
		return n
	}
	if reloads() != 1 {
		t.Fatalf("daemon-reload x%d", reloads())
	}

	// Nothing left to do: no write, no reload.
	if res, err := o.poolUnitsRefresh(context.Background()); err != nil || len(res.Updated) != 0 || reloads() != 1 {
		t.Fatalf("second refresh = %+v %v (reloads %d)", res, err, reloads())
	}
}

func TestEngineConfigRefresh(t *testing.T) {
	run := sysexec.NewFake().
		Set(sysexec.Result{Stdout: []byte("active\n")}, "systemctl", "is-active", "docker.service").
		Set(sysexec.Result{}, "systemctl", "reload", "docker.service")
	files := NewMemFiles()
	files.MkdirAll("/etc/docker", 0o755)
	// What the helper wrote before ADR-0029.
	files.WriteFile("/etc/docker/daemon.json", []byte(`{"data-root":"/srv/cwnsb/tank/.docker","live-restore":true,"log-driver":"local"}`), 0o644)
	o := &Ops{Run: run, Files: files, Logger: quietLogger(), Sleep: func(time.Duration) {}}

	res, err := o.engineConfigRefresh(context.Background())
	b, _ := files.ReadFile("/etc/docker/daemon.json")
	if err != nil || !res.Updated || !res.Reloaded || !strings.Contains(string(b), `"live-restore": false`) {
		t.Fatalf("refresh = %+v %v: %s", res, err, b)
	}
	// Reloaded, never restarted: the apps keep running.
	if slices.ContainsFunc(run.Calls(), func(c []string) bool { return slices.Contains(c, "restart") }) {
		t.Error("Docker restarted")
	}
	if res, err := o.engineConfigRefresh(context.Background()); err != nil || res.Updated {
		t.Fatalf("second refresh = %+v %v", res, err)
	}

	// Not ours (data on the SD card): never touched.
	theirs := `{"live-restore":true}`
	files.WriteFile("/etc/docker/daemon.json", []byte(theirs), 0o644)
	if res, _ := o.engineConfigRefresh(context.Background()); res.Updated {
		t.Fatal("a foreign daemon.json was rewritten")
	}
}
