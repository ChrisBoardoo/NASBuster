// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/crosswax/nasbuster/internal/apps"
	"github.com/crosswax/nasbuster/internal/helper"
	"github.com/crosswax/nasbuster/internal/store"
)

func TestAppsFlow(t *testing.T) {
	e := newEnv(t)
	e.setup()
	start := time.Now()
	e.sys.Clock = func() time.Time { return start }
	e.createPool("tank", "mirror", demoSDA, demoSDD)

	var engine helper.EngineStatus
	e.do("GET", "/api/v1/apps/engine", nil, http.StatusOK, &engine)
	if engine.Installed || engine.Active {
		t.Fatalf("fresh engine = %+v", engine)
	}
	var catalog struct{ Templates []apps.Template }
	e.do("GET", "/api/v1/apps/catalog", nil, http.StatusOK, &catalog)
	if len(catalog.Templates) < 2 {
		t.Fatalf("catalog = %+v", catalog)
	}
	app := map[string]any{"name": "portainer", "pool": "tank", "template": "portainer"}
	if c := errCode(t, e, "POST", "/api/v1/apps", app, http.StatusConflict); c != "apps.engine_missing" {
		t.Fatalf("deploy without engine: %s", c)
	}

	// Install: Docker is configured to keep its data on the pool BEFORE the package starts it.
	e.do("POST", "/api/v1/apps/engine/install", map[string]any{"pool": "tank"}, http.StatusAccepted, nil)
	conf, err := e.sys.Files.ReadFile(apps.DaemonJSON)
	if err != nil || apps.EnginePool(conf) != "tank" {
		t.Fatalf("daemon.json = %s %v", conf, err)
	}
	if dropIn, _ := e.sys.Files.ReadFile("/etc/systemd/system/" + apps.DropInName); !strings.Contains(string(dropIn), "RequiresMountsFor=/srv/cwnsb/tank") {
		t.Fatalf("drop-in = %q", dropIn)
	}
	var job apps.Job
	e.do("GET", "/api/v1/jobs/engine", nil, http.StatusOK, &job)
	if job.State != "running" || len(job.Log) == 0 {
		t.Fatalf("install job = %+v", job)
	}
	if c := errCode(t, e, "POST", "/api/v1/apps/engine/install", map[string]any{"pool": "tank"}, http.StatusConflict); c != "apps.job_running" {
		t.Fatalf("second install: %s", c)
	}
	e.advance(30 * time.Second)
	e.do("GET", "/api/v1/apps/engine", nil, http.StatusOK, &engine)
	if !engine.Installed || !engine.Active || engine.Pool != "tank" || engine.Job.State != "succeeded" {
		t.Fatalf("installed engine = %+v", engine)
	}
	if c := errCode(t, e, "POST", "/api/v1/apps/engine/install", map[string]any{"pool": "tank"}, http.StatusConflict); c != "apps.engine_installed" {
		t.Fatalf("reinstall: %s", c)
	}

	// Validation.
	if c := errCode(t, e, "POST", "/api/v1/apps", map[string]any{"name": "Bad Name", "pool": "tank", "compose": "services: {}"}, http.StatusBadRequest); c != "validation.app_name" {
		t.Fatalf("bad name: %s", c)
	}
	if c := errCode(t, e, "POST", "/api/v1/apps", map[string]any{"name": "broken", "pool": "tank", "compose": "nope: ["}, http.StatusBadRequest); c != "apps.compose_invalid" {
		t.Fatalf("bad compose: %s", c)
	}
	if e.sys.Files.Exists("/srv/cwnsb/tank/.apps/broken") {
		t.Fatal("a refused application left its folder on the pool")
	}

	// Deploy Portainer from the catalog.
	e.do("POST", "/api/v1/apps", app, http.StatusAccepted, nil)
	if c := errCode(t, e, "POST", "/api/v1/apps", app, http.StatusConflict); c != "apps.exists" {
		t.Fatalf("duplicate: %s", c)
	}
	if !e.sys.Files.Exists("/srv/cwnsb/tank/.apps/portainer/compose.yaml") {
		t.Fatal("compose file not written on the pool")
	}
	var list struct {
		Engine helper.EngineStatus
		Apps   []appView
		Others []helper.ProjectView
	}
	e.do("GET", "/api/v1/apps", nil, http.StatusOK, &list)
	if len(list.Apps) != 1 || list.Apps[0].Job.State != "running" || list.Apps[0].Status != "" {
		t.Fatalf("while deploying = %+v", list.Apps)
	}
	e.advance(20 * time.Second)
	e.do("GET", "/api/v1/apps", nil, http.StatusOK, &list)
	a := list.Apps[0]
	if a.Status != "running(1)" || len(a.Containers) != 1 || len(a.Containers[0].Ports) != 1 || a.Containers[0].Ports[0].PublishedPort != 9443 {
		t.Fatalf("deployed = %+v", a)
	}

	// Stop, start, logs.
	e.do("POST", "/api/v1/apps/portainer/stop", nil, http.StatusOK, nil)
	e.do("GET", "/api/v1/apps", nil, http.StatusOK, &list)
	if !strings.HasPrefix(list.Apps[0].Status, "exited") {
		t.Fatalf("stopped = %+v", list.Apps[0])
	}
	e.do("POST", "/api/v1/apps/portainer/start", nil, http.StatusOK, nil)
	e.do("POST", "/api/v1/apps/portainer/explode", nil, http.StatusNotFound, nil)
	var logs struct{ Logs string }
	e.do("GET", "/api/v1/apps/portainer/logs?tail=50", nil, http.StatusOK, &logs)
	if !strings.Contains(logs.Logs, "portainer") {
		t.Fatalf("logs = %q", logs.Logs)
	}

	// The pool holds Docker's data and the app: it cannot be destroyed.
	if c := errCode(t, e, "DELETE", "/api/v1/storage/pools/tank", map[string]any{"confirm": "tank"}, http.StatusConflict); c != "storage.pool_not_empty" {
		t.Fatalf("destroy a pool holding apps: %s", c)
	}

	// Update: pull and recreate in the background.
	e.do("POST", "/api/v1/apps/portainer/update", nil, http.StatusAccepted, nil)
	var upd apps.Job
	e.do("GET", "/api/v1/jobs/app-portainer", nil, http.StatusOK, &upd)
	if upd.State != "running" {
		t.Fatalf("update job = %+v", upd)
	}
	e.advance(20 * time.Second)

	// Docker cannot be removed while an application exists.
	var inUse struct{ Error apiError }
	e.do("POST", "/api/v1/apps/engine/remove", map[string]any{"confirm": "docker"}, http.StatusConflict, &inUse)
	if inUse.Error.Code != "apps.engine_in_use" {
		t.Fatalf("remove engine with apps = %+v", inUse.Error)
	}

	// Remove, with its data.
	if c := errCode(t, e, "DELETE", "/api/v1/apps/portainer", map[string]any{"confirm": "port"}, http.StatusBadRequest); c != "validation.confirm" {
		t.Fatalf("bad confirm: %s", c)
	}
	e.do("DELETE", "/api/v1/apps/portainer", map[string]any{"confirm": "portainer", "deleteData": true}, http.StatusNoContent, nil)
	if e.sys.Files.Exists("/srv/cwnsb/tank/.apps/portainer") {
		t.Fatal("app data left behind")
	}
	// ... but kept a week in the pool's trash (ADR-0035).
	if kept, _ := e.sys.Files.ReadDir(apps.TrashDir("tank")); len(kept) != 1 || !strings.HasPrefix(kept[0], "app-portainer-") {
		t.Fatalf("trash = %q", kept)
	}
	e.do("GET", "/api/v1/apps", nil, http.StatusOK, &list)
	if len(list.Apps) != 0 {
		t.Fatalf("apps = %+v", list.Apps)
	}
	if c := errCode(t, e, "POST", "/api/v1/apps/engine/remove", map[string]any{"confirm": "yes"}, http.StatusBadRequest); c != "validation.confirm" {
		t.Fatalf("engine remove confirm: %s", c)
	}
	// Uninstall keeping the data: the pool holds ".docker" and says so.
	e.do("POST", "/api/v1/apps/engine/remove", map[string]any{"confirm": "docker"}, http.StatusOK, nil)
	var held struct{ Error apiError }
	e.do("DELETE", "/api/v1/storage/pools/tank", map[string]any{"confirm": "tank"}, http.StatusConflict, &held)
	if entries, _ := held.Error.Params["entries"].([]any); len(entries) != 1 || entries[0] != ".docker" {
		t.Fatalf("pool_not_empty = %+v", held.Error)
	}
	// Then delete that data, Docker being gone.
	e.do("POST", "/api/v1/apps/engine/remove", map[string]any{"confirm": "docker", "deleteData": true, "pool": "tank"}, http.StatusOK, nil)
	e.do("GET", "/api/v1/apps/engine", nil, http.StatusOK, &engine)
	if engine.Installed || e.sys.Files.Exists(apps.DaemonJSON) || e.sys.Files.Exists("/srv/cwnsb/tank/.docker") {
		t.Fatalf("after removal = %+v", engine)
	}
	// The pool no longer holds anything: it can be destroyed.
	e.do("DELETE", "/api/v1/storage/pools/tank", map[string]any{"confirm": "tank"}, http.StatusOK, nil)

	var audit struct {
		Entries []struct{ Action, Detail string }
	}
	e.do("GET", "/api/v1/audit?limit=5", nil, http.StatusOK, &audit)
	if b, _ := json.Marshal(audit); !strings.Contains(string(b), `"app.remove"`) || !strings.Contains(string(b), "data deleted") {
		t.Fatalf("audit = %s", b)
	}
}

// TestEngineRemoveKeepsAppsWithoutContainers: P10 (Pi 5, 2026-10-30). Docker
// was reinstalled on another pool, so the recorded applications have no
// container in it. It uninstalls without removing them first; they stay
// recorded, their data untouched (ADR-0035).
func TestEngineRemoveKeepsAppsWithoutContainers(t *testing.T) {
	e := newEnv(t)
	e.setup()
	start := time.Now()
	e.sys.Clock = func() time.Time { return start }
	e.createPool("tank", "mirror", demoSDA, demoSDD)
	e.do("POST", "/api/v1/apps/engine/install", map[string]any{"pool": "tank"}, http.StatusAccepted, nil)
	e.advance(30 * time.Second)
	e.do("POST", "/api/v1/apps", map[string]any{"name": "portainer", "pool": "tank", "template": "portainer"}, http.StatusAccepted, nil)
	e.advance(20 * time.Second)

	// With its container in this Docker, the application holds it back.
	var inUse struct{ Error apiError }
	e.do("POST", "/api/v1/apps/engine/remove", map[string]any{"confirm": "docker"}, http.StatusConflict, &inUse)
	if names, _ := inUse.Error.Params["apps"].([]any); len(names) != 1 || names[0] != "portainer" {
		t.Fatalf("in use = %+v", inUse.Error)
	}

	// Its containers live in another Docker: this one has none.
	if _, err := e.sys.Runner.Run(context.Background(), "docker", "compose", "-p", apps.Project("portainer"), "down"); err != nil {
		t.Fatal(err)
	}
	e.do("POST", "/api/v1/apps/engine/remove", map[string]any{"confirm": "docker"}, http.StatusOK, nil)
	var list struct{ Apps []appView }
	e.do("GET", "/api/v1/apps", nil, http.StatusOK, &list)
	if len(list.Apps) != 1 || list.Apps[0].Name != "portainer" {
		t.Fatalf("apps after uninstall = %+v", list.Apps)
	}
	if !e.sys.Files.Exists("/srv/cwnsb/tank/.apps/portainer/compose.yaml") {
		t.Fatal("app data touched")
	}
}

// TestEngineDataTakenBack: P9 (Pi 5, 2026-10-30). A new SD card: Docker is
// not installed, its data is still on tank. The engine status names tank,
// not e2, with its container; installed there, Docker takes the application
// back. Deleting that data leaves nothing to take back.
func TestEngineDataTakenBack(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.setup()
	start := time.Now()
	e.sys.Clock = func() time.Time { return start }
	e.createPool("e2", "single", demoSDC)
	e.createPool("tank", "mirror", demoSDA, demoSDD)
	e.do("POST", "/api/v1/apps/engine/install", map[string]any{"pool": "tank"}, http.StatusAccepted, nil)
	e.advance(30 * time.Second)
	e.do("POST", "/api/v1/apps", map[string]any{"name": "uptime-kuma", "pool": "tank", "template": "uptime-kuma"}, http.StatusAccepted, nil)
	e.advance(20 * time.Second)
	var engine helper.EngineStatus
	e.do("GET", "/api/v1/apps/engine", nil, http.StatusOK, &engine)
	if len(engine.DataPools) != 0 {
		t.Fatalf("installed, data pools = %+v", engine.DataPools)
	}

	// The SD card dies: Docker is gone from the system, its data stays on tank.
	if _, err := e.sys.Runner.Run(ctx, "apt-get", "purge", "-y", "-qq", "docker.io"); err != nil {
		t.Fatal(err)
	}
	e.sys.Files.Remove(apps.DaemonJSON)
	e.do("GET", "/api/v1/apps/engine", nil, http.StatusOK, &engine)
	if engine.Installed || len(engine.DataPools) != 1 || engine.DataPools[0] != (helper.EngineData{Pool: "tank", Containers: 1}) {
		t.Fatalf("data pools = %+v", engine)
	}

	e.do("POST", "/api/v1/apps/engine/install", map[string]any{"pool": "tank"}, http.StatusAccepted, nil)
	e.advance(30 * time.Second)
	var list struct{ Apps []appView }
	e.do("GET", "/api/v1/apps", nil, http.StatusOK, &list)
	if len(list.Apps) != 1 || len(list.Apps[0].Containers) != 1 || list.Apps[0].Containers[0].State != "running" {
		t.Fatalf("apps after reinstall = %+v", list.Apps)
	}

	// Uninstalled with its data, then reinstalled: nothing comes back.
	e.sys.Runner.Run(ctx, "docker", "compose", "-p", apps.Project("uptime-kuma"), "stop")
	e.sys.Runner.Run(ctx, "apt-get", "purge", "-y", "-qq", "docker.io")
	e.do("POST", "/api/v1/apps/engine/remove", map[string]any{"confirm": "docker", "deleteData": true, "pool": "tank"}, http.StatusOK, nil)
	e.do("GET", "/api/v1/apps/engine", nil, http.StatusOK, &engine)
	if len(engine.DataPools) != 0 {
		t.Fatalf("data deleted, data pools = %+v", engine.DataPools)
	}
}

// TestOrphanApps: P13 (Pi 5, 2026-10-30). A CWNSB project whose application
// is not recorded (here: as after restoring a configuration older than it)
// is listed as an orphan with its pool, then stopped and removed, its files
// untouched (ADR-0036).
func TestOrphanApps(t *testing.T) {
	var st *store.Store
	e := newEnvWith(t, func(d *Deps) { st = d.Store })
	e.setup()
	start := time.Now()
	e.sys.Clock = func() time.Time { return start }
	e.createPool("tank", "mirror", demoSDA, demoSDD)
	e.do("POST", "/api/v1/apps/engine/install", map[string]any{"pool": "tank"}, http.StatusAccepted, nil)
	e.advance(30 * time.Second)
	e.do("POST", "/api/v1/apps", map[string]any{"name": "uptime-kuma", "pool": "tank", "template": "uptime-kuma"}, http.StatusAccepted, nil)
	e.advance(20 * time.Second)
	if !e.sys.Files.Exists("/srv/cwnsb/tank/.apps/uptime-kuma/data") {
		t.Fatal("data folder not created by the deployment")
	}

	// A recorded application keeps its own actions.
	e.do("POST", "/api/v1/apps/orphans/uptime-kuma/stop", nil, http.StatusConflict, nil)

	if err := st.DeleteApp(context.Background(), "uptime-kuma"); err != nil {
		t.Fatal(err)
	}
	var list struct {
		Apps    []appView
		Orphans []orphanView
	}
	state := func() string {
		e.do("GET", "/api/v1/apps", nil, http.StatusOK, &list)
		if len(list.Apps) != 0 {
			t.Fatalf("apps = %+v", list.Apps)
		}
		if len(list.Orphans) == 0 {
			return "none"
		}
		o := list.Orphans[0]
		if len(list.Orphans) != 1 || o.App != "uptime-kuma" || o.Pool != "tank" || len(o.Containers) != 1 {
			t.Fatalf("orphans = %+v", list.Orphans)
		}
		return o.Containers[0].State
	}
	if s := state(); s != "running" {
		t.Fatalf("orphan = %s", s)
	}
	e.do("POST", "/api/v1/apps/orphans/uptime-kuma/stop", nil, http.StatusOK, nil)
	if s := state(); s != "exited" {
		t.Fatalf("after stop = %s", s)
	}
	e.do("POST", "/api/v1/apps/orphans/uptime-kuma/remove", nil, http.StatusOK, nil)
	if s := state(); s != "none" {
		t.Fatalf("after remove = %s", s)
	}
	if !e.sys.Files.Exists("/srv/cwnsb/tank/.apps/uptime-kuma/compose.yaml") || !e.sys.Files.Exists("/srv/cwnsb/tank/.apps/uptime-kuma/data") {
		t.Fatal("the orphan's files were touched")
	}
	e.do("POST", "/api/v1/apps/orphans/uptime-kuma/purge", nil, http.StatusNotFound, nil)
	e.do("POST", "/api/v1/apps/orphans/Kuma/stop", nil, http.StatusNotFound, nil)

	var audit struct {
		Entries []struct{ Action, Target string }
	}
	e.do("GET", "/api/v1/audit?limit=5", nil, http.StatusOK, &audit)
	if b, _ := json.Marshal(audit); !strings.Contains(string(b), `"app.orphan_remove"`) || !strings.Contains(string(b), `"app.orphan_stop"`) {
		t.Fatalf("audit = %s", b)
	}
}

// Portainer's setup token (2.45): found in its logs and shown only while it
// can be used; the administrator creates the account in Portainer.
func TestAppSetupToken(t *testing.T) {
	adminCreated := false
	var checked []int
	e := newEnvWith(t, func(d *Deps) {
		d.AdminCheck = func(_ context.Context, scheme string, port int) (bool, error) {
			if scheme != "https" {
				t.Errorf("admin check over %s", scheme)
			}
			checked = append(checked, port)
			return adminCreated, nil
		}
	})
	e.setup()
	start := time.Now()
	e.sys.Clock = func() time.Time { return start }
	e.createPool("tank", "mirror", demoSDA, demoSDD)
	e.do("POST", "/api/v1/apps/engine/install", map[string]any{"pool": "tank"}, http.StatusAccepted, nil)
	e.advance(30 * time.Second)
	e.do("POST", "/api/v1/apps", map[string]any{"name": "portainer", "pool": "tank", "template": "portainer"}, http.StatusAccepted, nil)
	e.do("POST", "/api/v1/apps", map[string]any{"name": "uptime-kuma", "pool": "tank", "template": "uptime-kuma"}, http.StatusAccepted, nil)

	type view struct {
		State     string
		Token     string
		ExpiresAt *time.Time
	}
	get := func() view {
		t.Helper()
		var v view
		e.do("GET", "/api/v1/apps/portainer/setup-token", nil, http.StatusOK, &v)
		return v
	}
	if v := get(); v.State != "none" || v.Token != "" { // still deploying
		t.Fatalf("deploying: %+v", v)
	}
	e.advance(20 * time.Second)
	deployed := e.sys.Clock()
	e.do("GET", "/api/v1/apps", nil, http.StatusOK, nil) // ends the deployment jobs
	v := get()
	if v.State != "waiting" || len(v.Token) != 64 || v.ExpiresAt == nil || v.ExpiresAt.Before(deployed) || v.ExpiresAt.After(deployed.Add(5*time.Minute)) {
		t.Fatalf("waiting: %+v (deployed %v)", v, deployed)
	}
	if len(checked) == 0 || checked[len(checked)-1] != 9443 {
		t.Fatalf("admin checked on %v", checked)
	}
	first := v.Token
	if c := errCode(t, e, "GET", "/api/v1/apps/uptime-kuma/setup-token", nil, http.StatusNotFound); c != "apps.no_setup_token" {
		t.Fatalf("uptime-kuma: %s", c)
	}

	// Nobody created the account: Portainer locks itself, the token is gone.
	e.advance(5*time.Minute + time.Second)
	if v := get(); v.State != "locked" || v.Token != "" || v.ExpiresAt != nil {
		t.Fatalf("locked: %+v", v)
	}
	// A restart prints a new token.
	e.do("POST", "/api/v1/apps/portainer/restart", nil, http.StatusOK, nil)
	v = get()
	if v.State != "waiting" || len(v.Token) != 64 || v.Token == first {
		t.Fatalf("after restart: %+v (first %s)", v, first)
	}
	// The administrator created the account in Portainer: nothing more to show.
	adminCreated = true
	if v := get(); v.State != "created" || v.Token != "" {
		t.Fatalf("created: %+v", v)
	}
	adminCreated = false
	e.do("POST", "/api/v1/apps/portainer/stop", nil, http.StatusOK, nil)
	if v := get(); v.State != "none" {
		t.Fatalf("stopped: %+v", v)
	}
}
