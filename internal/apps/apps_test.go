// SPDX-License-Identifier: AGPL-3.0-or-later

package apps

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Outputs captured in the lab (Docker 26.1.5, Compose 2.26.1, tools/lab/probe-docker.sh).
const (
	labLs = `[{"Name":"cwnsbprobe","Status":"running(1)","ConfigFiles":"/var/lib/cwnsb-lab/probe-docker/compose.yaml"}]`
	labPs = `{"Command":"\"/docker-entrypoint.…\"","CreatedAt":"2026-10-02 00:49:34 +0200 CEST","ExitCode":0,"Health":"","ID":"817a576c6f75","Image":"nginx:alpine","Labels":"com.docker.compose.project=cwnsbprobe","LocalVolumes":"0","Mounts":"/var/lib/cwnsb…","Name":"cwnsbprobe-web-1","Names":"cwnsbprobe-web-1","Networks":"cwnsbprobe_default","Ports":"0.0.0.0:18081->80/tcp, :::18081->80/tcp","Project":"cwnsbprobe","Publishers":[{"URL":"0.0.0.0","TargetPort":80,"PublishedPort":18081,"Protocol":"tcp"},{"URL":"::","TargetPort":80,"PublishedPort":18081,"Protocol":"tcp"}],"RunningFor":"3 seconds ago","Service":"web","Size":"0B","State":"running","Status":"Up 2 seconds"}`
)

func TestNamesAndPaths(t *testing.T) {
	for _, ok := range []string{"portainer", "uptime-kuma", "a", "jelly2"} {
		if !ValidName(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"", "Portainer", "-x", "x-", "a_b", "../x", "a b", strings.Repeat("a", 31), "9lives"} {
		if ValidName(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
	if AppDir("tank", "portainer") != "/srv/cwnsb/tank/.apps/portainer" || EngineDir("tank") != "/srv/cwnsb/tank/.docker" {
		t.Fatal("paths")
	}
	if !ValidJob("engine") || !ValidJob("app-portainer") || ValidJob("app-") || ValidJob("x") || ValidJob("app-../x") {
		t.Fatal("ValidJob")
	}
	// Long pool operations (ADR-0026): the suffix is a pool name.
	if !ValidJob(PoolJob(ScrubJob, "tank")) || !ValidJob("resync-e1") || ValidJob("scrub-") || ValidJob("scrub-Tank") || ValidJob("balance-../x") {
		t.Fatal("ValidJob (pool jobs)")
	}
}

// TestTrashEntry: a removed application's data is kept under a name that
// says what it was and when (ADR-0035); anything else in the folder is left alone.
func TestTrashEntry(t *testing.T) {
	at := time.Date(2026, 10, 30, 10, 18, 5, 0, time.FixedZone("CET", 3600))
	if TrashDir("tank") != "/srv/cwnsb/tank/.cwnsb/trash" {
		t.Fatal(TrashDir("tank"))
	}
	entry := TrashEntry("uptime-kuma", at)
	if entry != "app-uptime-kuma-20261030T091805Z" {
		t.Fatal(entry)
	}
	name, when, ok := ParseTrashEntry(entry)
	if !ok || name != "uptime-kuma" || !when.Equal(at) {
		t.Fatalf("parse = %q %v %v", name, when, ok)
	}
	for _, bad := range []string{"", "app-", "app--20261030T091805Z", "uptime-kuma-20261030T091805Z",
		"app-Kuma-20261030T091805Z", "app-kuma-2026-10-30", "app-../x-20261030T091805Z", "app-kuma-20261030T091805Z.tmp"} {
		if _, _, ok := ParseTrashEntry(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}

// `docker compose config --format json` (Compose 2.26.1, Debian 13), for the
// Portainer template and a pasted file mixing every kind of mount.
const (
	configPortainer = `{"name":"cwnsb-portainer","services":{"portainer":{"command":null,"entrypoint":null,"image":"portainer/portainer-ce:lts",` +
		`"ports":[{"mode":"ingress","target":9443,"published":"9443","protocol":"tcp"}],"restart":"unless-stopped","volumes":[` +
		`{"type":"bind","source":"/var/run/docker.sock","target":"/var/run/docker.sock","bind":{}},` +
		`{"type":"bind","source":"/srv/cwnsb/tank/.apps/portainer/data","target":"/data","bind":{}}]}}}`
	configMixed = `{"name":"cwnsb-mixed","networks":{"default":{"name":"cwnsb-mixed_default","ipam":{}}},"services":{"web":{"command":null,"entrypoint":null,"image":"nginx:alpine","networks":{"default":null},"volumes":[` +
		`{"type":"bind","source":"/srv/cwnsb/tank/.apps/mixed/conf/nginx","target":"/etc/nginx/conf.d","read_only":true,"bind":{"create_host_path":true}},` +
		`{"type":"bind","source":"/srv/cwnsb/tank/media","target":"/media","bind":{"create_host_path":true}},` +
		`{"type":"bind","source":"/srv/cwnsb/tank/.apps/other/data","target":"/other","bind":{"create_host_path":true}},` +
		`{"type":"volume","source":"cache","target":"/var/cache/nginx","volume":{}}]},` +
		`"worker":{"command":null,"entrypoint":null,"image":"nginx:alpine","networks":{"default":null},"volumes":[` +
		`{"type":"bind","source":"/srv/cwnsb/tank/.apps/mixed/conf/nginx","target":"/etc/nginx/conf.d","read_only":true,"bind":{"create_host_path":true}}]}},` +
		`"volumes":{"cache":{"name":"cwnsb-mixed_cache"}}}`
)

// TestBindSources: the folders an application binds, and which of them lie
// inside its own folder (the only ones NasBuster creates, ADR-0036).
func TestBindSources(t *testing.T) {
	got, err := BindSources([]byte(configPortainer))
	if err != nil || strings.Join(got, " ") != "/srv/cwnsb/tank/.apps/portainer/data /var/run/docker.sock" {
		t.Fatalf("portainer = %q %v", got, err)
	}
	got, err = BindSources([]byte(configMixed))
	want := "/srv/cwnsb/tank/.apps/mixed/conf/nginx /srv/cwnsb/tank/.apps/other/data /srv/cwnsb/tank/media"
	if err != nil || strings.Join(got, " ") != want {
		t.Fatalf("mixed = %q %v", got, err)
	}
	if _, err := BindSources([]byte("services:\n")); err == nil {
		t.Fatal("YAML accepted as JSON")
	}
	for src, want := range map[string]string{
		"/srv/cwnsb/tank/.apps/mixed/conf/nginx": "conf/nginx",
		"/srv/cwnsb/tank/.apps/mixed/data/":      "data",
		"/srv/cwnsb/tank/.apps/mixed":            "",
		"/srv/cwnsb/tank/.apps/mixed2/data":      "",
		"/srv/cwnsb/tank/.apps/other/data":       "",
		"/srv/cwnsb/tank/.apps/mixed/../x":       "",
		"/srv/cwnsb/e2/.apps/mixed/data":         "",
		"/var/run/docker.sock":                   "",
	} {
		rel, ok := InAppDir("tank", "mixed", src)
		if rel != want || ok != (want != "") {
			t.Errorf("InAppDir(%s) = %q %v, want %q", src, rel, ok, want)
		}
	}
}

// TestProjectPool: an orphan project's pool, read from the compose file
// Docker remembers, only where NasBuster puts it.
func TestProjectPool(t *testing.T) {
	for files, want := range map[string]string{
		"/srv/cwnsb/tank/.apps/uptime-kuma/compose.yaml":  "tank",
		"/srv/cwnsb/e2/.apps/uptime-kuma/compose.yaml":    "e2",
		"/srv/cwnsb/tank/.apps/portainer/compose.yaml":    "",
		"/srv/cwnsb/tank/.apps/uptime-kuma/.compose.yaml": "",
		"/srv/cwnsb/Tank/.apps/uptime-kuma/compose.yaml":  "",
		"/home/pi/uptime-kuma/compose.yaml":               "",
		"":                                                "",
	} {
		if got := ProjectPool("uptime-kuma", files); got != want {
			t.Errorf("ProjectPool(%q) = %q, want %q", files, got, want)
		}
	}
	// Several files (an override added by hand): not NasBuster's layout.
	two := "/srv/cwnsb/tank/.apps/uptime-kuma/compose.yaml,/srv/cwnsb/tank/.apps/uptime-kuma/override.yaml"
	if got := ProjectPool("uptime-kuma", two); got != "" {
		t.Errorf("ProjectPool(two files) = %q", got)
	}
}

func TestDaemonConfig(t *testing.T) {
	b := DaemonConfig("tank")
	var c map[string]any
	if err := json.Unmarshal(b, &c); err != nil || c["data-root"] != "/srv/cwnsb/tank/.docker" || c["log-driver"] != "local" || c["live-restore"] != false {
		t.Fatalf("daemon.json = %s %v", b, err)
	}
	// An existing installation: only live-restore changes (ADR-0029), a
	// registry mirror the user added stays.
	old := []byte(`{"data-root":"/srv/cwnsb/tank/.docker","live-restore":true,"registry-mirrors":["https://mirror.example"]}`)
	nb, ok := WithoutLiveRestore(old)
	var n map[string]any
	if !ok || json.Unmarshal(nb, &n) != nil || n["live-restore"] != false || n["registry-mirrors"] == nil || EnginePool(nb) != "tank" {
		t.Fatalf("without live-restore = %s %v", nb, ok)
	}
	if _, ok := WithoutLiveRestore(nb); ok {
		t.Error("nothing left to change, yet a rewrite")
	}
	if _, ok := WithoutLiveRestore([]byte("{not json")); ok {
		t.Error("invalid JSON rewritten")
	}
	if EnginePool(b) != "tank" || EnginePool([]byte(`{"data-root":"/var/lib/docker"}`)) != "" || EnginePool([]byte(`{"data-root":"/srv/cwnsb/tank/x"}`)) != "" {
		t.Fatal("EnginePool")
	}
	if !strings.Contains(DropIn("tank"), "RequiresMountsFor=/srv/cwnsb/tank\n") {
		t.Fatal(DropIn("tank"))
	}
}

func TestParseCompose(t *testing.T) {
	projects, err := ParseProjects([]byte(labLs))
	if err != nil || len(projects) != 1 || projects[0].Status != "running(1)" {
		t.Fatalf("ls = %+v %v", projects, err)
	}
	cs, err := ParseContainers([]byte(labPs + "\n" + strings.Replace(labPs, "cwnsbprobe-web-1", "cwnsbprobe-db-1", 2) + "\n"))
	if err != nil || len(cs) != 2 || cs[1].Name != "cwnsbprobe-web-1" || cs[1].State != "running" {
		t.Fatalf("ps = %+v %v", cs, err)
	}
	if len(cs[1].Ports) != 1 || cs[1].Ports[0].PublishedPort != 18081 {
		t.Fatalf("ports (IPv4 + IPv6 are one port) = %+v", cs[1].Ports)
	}
	if arr, err := ParseContainers([]byte("[" + labPs + "]")); err != nil || len(arr) != 1 {
		t.Fatalf("array form = %+v %v", arr, err)
	}
	if empty, err := ParseContainers([]byte("")); err != nil || len(empty) != 0 {
		t.Fatalf("empty = %+v %v", empty, err)
	}
}

func TestParseJob(t *testing.T) {
	cases := map[string]string{
		"LoadState=not-found\nActiveState=inactive\nResult=success\nExecMainStatus=0": "none",
		"LoadState=loaded\nActiveState=activating\nResult=success\nExecMainStatus=0":  "running",
		"LoadState=loaded\nActiveState=active\nResult=success\nExecMainStatus=0":      "succeeded",
		"LoadState=loaded\nActiveState=failed\nResult=exit-code\nExecMainStatus=100":  "failed",
		"LoadState=loaded\nActiveState=inactive\nResult=exit-code\nExecMainStatus=1":  "failed",
	}
	for in, want := range cases {
		if got := ParseJob(in); got.State != want {
			t.Errorf("%q -> %s, want %s", in, got.State, want)
		}
	}
	if j := ParseJob("LoadState=loaded\nActiveState=failed\nResult=exit-code\nExecMainStatus=100"); j.ExitStatus != 100 {
		t.Fatal(j)
	}
}

func TestCatalog(t *testing.T) {
	c := Catalog()
	if len(c) < 2 {
		t.Fatalf("catalog = %+v", c)
	}
	p, ok := TemplateByName("portainer")
	if !ok || !p.Expert || p.WebPort != 9443 || !strings.Contains(p.Compose, "portainer/portainer-ce:lts") || p.Description["fr"] == "" {
		t.Fatalf("portainer = %+v", p)
	}
	// Uptime Kuma 1 no longer gets fixes, and warns about it on first visit.
	if k, ok := TemplateByName("uptime-kuma"); !ok || !strings.Contains(k.Compose, "image: louislam/uptime-kuma:2\n") {
		t.Fatalf("uptime-kuma = %+v", k)
	}
	image := regexp.MustCompile(`(?m)^\s*image:\s*(\S+)\s*$`)
	shortBind := regexp.MustCompile(`(?m)^\s*-\s*["']?[./~][^:\s]*:`)
	for _, tpl := range c {
		if !ValidName(tpl.Name) || len(tpl.Compose) > MaxCompose || !strings.Contains(tpl.Compose, "services:") {
			t.Errorf("template %s invalid", tpl.Name)
		}
		// Docker must never recreate an application's folder empty (P13):
		// bind mounts in the long syntax, create_host_path: false.
		if shortBind.MatchString(tpl.Compose) || strings.Count(tpl.Compose, "type: bind") != strings.Count(tpl.Compose, "create_host_path: false") {
			t.Errorf("template %s: bind mounts need the long syntax with create_host_path: false", tpl.Name)
		}
		for _, m := range image.FindAllStringSubmatch(tpl.Compose, -1) {
			ref := m[1]
			if i := strings.LastIndex(ref, ":"); i < 0 || strings.Contains(ref[i:], "/") || ref[i+1:] == "latest" {
				t.Errorf("template %s: image %s needs a maintained tag, not latest", tpl.Name, ref)
			}
		}
	}
}
