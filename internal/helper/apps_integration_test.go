// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build integration && linux

// Real Docker in the cwnsb-lab distro (needs Internet for apt and the image):
// the engine is installed through the helper, used, then purged.
package helper

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/crosswax/nasbuster/internal/apps"
	"github.com/crosswax/nasbuster/internal/storage"
)

func (l *labPool) waitJob(job string, limit time.Duration) apps.Job {
	l.t.Helper()
	deadline := time.Now().Add(limit)
	for {
		j := l.ops.job(context.Background(), job, 40)
		if j.State == "succeeded" {
			return j
		}
		if j.State == "failed" || time.Now().After(deadline) {
			l.t.Fatalf("job %s = %s:\n%s", job, j.State, strings.Join(j.Log, "\n"))
		}
		time.Sleep(time.Second)
	}
}

func TestLabApps(t *testing.T) {
	ctx := context.Background()
	l := newLabPool(t, 2)
	if l.ops.engineStatus(ctx).Installed {
		t.Skip("Docker is already installed in the lab: purge it first")
	}
	pool, _, err := l.create(storage.LayoutMirror, 0, 1)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Runs before the pool cleanup (LIFO): Docker must let go of the pool first.
	t.Cleanup(func() {
		l.run.Run(ctx, "docker", "compose", "-p", apps.Project("web"), "down")
		l.run.Run(ctx, "systemctl", "stop", "docker.socket", "docker.service", "containerd.service")
		l.run.Run(ctx, "systemctl", "stop", apps.JobUnit(apps.EngineJob), apps.JobUnit("app-web"))
		l.run.Run(ctx, "systemctl", "reset-failed", apps.JobUnit(apps.EngineJob), apps.JobUnit("app-web"))
		l.run.Run(ctx, "apt-get", "purge", "-y", "-qq", "docker.io", "docker-cli", "docker-compose", "containerd", "runc", "tini")
		l.run.Run(ctx, "apt-get", "autoremove", "-y", "-qq")
		os.RemoveAll("/etc/docker")
		os.RemoveAll(filepath.Join(l.ops.UnitDir, "docker.service.d"))
		os.RemoveAll("/var/lib/docker")
		os.RemoveAll("/var/lib/containerd")
		l.run.Run(ctx, "systemctl", "daemon-reload")
		l.run.Run(ctx, "btrfs", "subvolume", "delete", apps.EngineDir(pool))
		os.RemoveAll(apps.AppsDir(pool))
	})

	// Install through a transient unit; data on the pool from the first start.
	if _, err := l.ops.engineInstall(ctx, EngineInstallArgs{Pool: pool}); err != nil {
		t.Fatalf("install: %v", err)
	}
	l.waitJob(apps.EngineJob, 10*time.Minute)
	st := l.ops.engineStatus(ctx)
	if !st.Installed || !st.Active || st.Pool != pool {
		t.Fatalf("engine = %+v", st)
	}
	res, _ := l.run.Run(ctx, "docker", "info", "--format", "{{.DockerRootDir}} {{.Driver}}")
	if got := strings.TrimSpace(string(res.Stdout)); got != apps.EngineDir(pool)+" overlay2" {
		t.Fatalf("docker info = %q", got)
	}
	if _, err := os.Stat("/var/lib/docker"); !os.IsNotExist(err) {
		t.Fatalf("Docker wrote to the system disk: %v", err)
	}

	// A broken compose file is refused before anything runs.
	if _, err := l.ops.appDeploy(ctx, AppDeployArgs{Pool: pool, Name: "broken", Compose: "services: ["}); errCode(err) != CodeComposeInvalid {
		t.Fatalf("broken compose: %v", err)
	}
	if _, err := os.Stat(apps.AppDir(pool, "broken")); !os.IsNotExist(err) {
		t.Fatalf("a refused application left its folder: %v", err)
	}

	// The catalog's bind syntax (ADR-0036): Docker may not create ./html, the
	// deployment does.
	compose := "services:\n  web:\n    image: nginx:alpine\n    restart: unless-stopped\n    ports:\n      - \"18095:80\"\n    volumes:\n" +
		"      - type: bind\n        source: ./html\n        target: /usr/share/nginx/html\n        read_only: true\n        bind:\n          create_host_path: false\n"
	if _, err := l.ops.appDeploy(ctx, AppDeployArgs{Pool: pool, Name: "web", Compose: compose}); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	l.waitJob("app-web", 5*time.Minute)
	html := filepath.Join(apps.AppDir(pool, "web"), "html")
	if fi, err := os.Lstat(html); err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o755 {
		t.Fatalf("bind source not created by the deployment: %v %v", fi, err)
	}
	if err := os.WriteFile(filepath.Join(html, "index.html"), []byte("lab app on the pool\n"), 0o644); err != nil {
		t.Fatalf("app data on the pool: %v", err)
	}
	body := ""
	for i := 0; i < 20 && !strings.Contains(body, "lab app"); i++ {
		if r, err := http.Get("http://127.0.0.1:18095/"); err == nil {
			b, _ := io.ReadAll(r.Body)
			r.Body.Close()
			body = string(b)
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !strings.Contains(body, "lab app") {
		t.Fatalf("app answered %q", body)
	}

	status, err := l.ops.appsStatus(ctx)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	var web *ProjectView
	for i := range status.Projects {
		if status.Projects[i].App == "web" {
			web = &status.Projects[i]
		}
	}
	if web == nil || len(web.Containers) != 1 || web.Containers[0].State != "running" || len(web.Containers[0].Ports) != 1 || web.Containers[0].Ports[0].PublishedPort != 18095 {
		t.Fatalf("status = %+v", status.Projects)
	}
	if logs, err := l.ops.appLogs(ctx, AppLogsArgs{Pool: pool, Name: "web", Tail: 50}); err != nil || !strings.Contains(logs["logs"], "web-1") {
		t.Fatalf("logs = %q %v", logs, err)
	}
	if _, err := l.ops.appAction(ctx, AppActionArgs{Pool: pool, Name: "web", Action: "stop"}); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if _, err := l.ops.appAction(ctx, AppActionArgs{Pool: pool, Name: "web", Action: "start"}); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := l.ops.appUpdate(ctx, AppActionArgs{Pool: pool, Name: "web"}); err != nil {
		t.Fatalf("update: %v", err)
	}
	l.waitJob("app-web", 5*time.Minute)
	if _, err := l.ops.engineRemove(ctx, EngineRemoveArgs{}); errCode(err) != CodeEngineInUse {
		t.Fatalf("engine removal with a container: %v", err)
	}

	// P13 (ADR-0036): Docker restarts the container by itself, but never
	// recreates its folder: gone, the application stays down instead of
	// coming back empty.
	moved := html + ".moved"
	l.run.Run(ctx, "systemctl", "stop", "docker.socket", "docker.service")
	if err := os.Rename(html, moved); err != nil {
		t.Fatal(err)
	}
	l.run.Run(ctx, "systemctl", "start", "docker.service")
	webState := func() string {
		st, err := l.ops.appsStatus(ctx)
		if err != nil {
			t.Fatalf("status: %v", err)
		}
		for _, p := range st.Projects {
			if p.App == "web" && len(p.Containers) == 1 {
				return p.Containers[0].State
			}
		}
		return "none"
	}
	time.Sleep(10 * time.Second) // Docker's restart policy has had its chance
	if state := webState(); state == "running" || state == "none" {
		t.Fatalf("after a restart without its folder, web is %s", state)
	}
	if _, err := os.Lstat(html); !os.IsNotExist(err) {
		t.Fatalf("Docker recreated the folder: %v", err)
	}
	if err := os.Rename(moved, html); err != nil {
		t.Fatal(err)
	}

	// An orphan (no record left: the API's view) is stopped, then removed,
	// by its project name only.
	if _, err := l.ops.appAction(ctx, AppActionArgs{Pool: pool, Name: "web", Action: "start"}); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := l.ops.appOrphan(ctx, OrphanArgs{Name: "web", Action: "stop"}); err != nil {
		t.Fatalf("orphan stop: %v", err)
	}
	if state := webState(); state != "exited" {
		t.Fatalf("after the orphan's stop, web is %s", state)
	}
	if _, err := l.ops.appOrphan(ctx, OrphanArgs{Name: "web", Action: "remove"}); err != nil {
		t.Fatalf("orphan remove: %v", err)
	}
	if state := webState(); state != "none" {
		t.Fatalf("after the orphan's removal, web is %s", state)
	}
	if _, err := os.Stat(filepath.Join(html, "index.html")); err != nil {
		t.Fatalf("the orphan's removal touched its files: %v", err)
	}
	if _, err := l.ops.appRemove(ctx, AppRemoveArgs{Pool: pool, Name: "web", DeleteData: true}); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Stat(apps.AppsDir(pool)); !os.IsNotExist(err) {
		t.Fatalf("empty .apps left on the pool: %v", err)
	}
	if res, _ := l.run.Run(ctx, "docker", "compose", "ls", "--all", "--format", "json"); strings.Contains(string(res.Stdout), "cwnsb-web") {
		t.Fatalf("project left: %s", res.Stdout)
	}

	// Uninstall Docker with its data: the pool is clean again.
	if _, err := l.ops.engineRemove(ctx, EngineRemoveArgs{DeleteData: true}); err != nil {
		t.Fatalf("engine removal: %v", err)
	}
	if st := l.ops.engineStatus(ctx); st.Installed || st.Active {
		t.Fatalf("after removal = %+v", st)
	}
	for _, p := range []string{apps.DaemonJSON, apps.EngineDir(pool), filepath.Join(l.ops.UnitDir, apps.DropInName)} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s left behind: %v", p, err)
		}
	}
}
