// SPDX-License-Identifier: AGPL-3.0-or-later

package helper

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/crosswax/nasbuster/internal/sysexec"
)

// newDockerOps: tank is mounted and Docker runs; `docker compose config`
// answers config (Compose 2.26.1's JSON), every other docker call succeeds.
func newDockerOps(files *MemFiles, config string) (*Ops, *sysexec.Fake) {
	run := sysexec.NewFake()
	run.Handle("findmnt", func(args []string) (sysexec.Result, error) {
		if args[len(args)-1] != "/srv/cwnsb/tank" {
			return sysexec.Result{ExitCode: 1}, nil
		}
		return sysexec.Result{Stdout: []byte(`{"filesystems":[{"target":"/srv/cwnsb/tank","source":"/dev/sda1","fstype":"btrfs","uuid":"u"}]}`)}, nil
	})
	run.Handle("dpkg-query", func([]string) (sysexec.Result, error) {
		return sysexec.Result{Stdout: []byte("docker.io installed 26.1.5+dfsg1-9+deb13u1\ndocker-cli installed 26.1.5+dfsg1-9+deb13u1\ndocker-compose installed 2.26.1-4\n")}, nil
	})
	run.Handle("systemctl", func(args []string) (sysexec.Result, error) {
		switch args[0] {
		case "is-active":
			return sysexec.Result{Stdout: []byte("active\n")}, nil
		case "show":
			return sysexec.Result{Stdout: []byte("LoadState=not-found\nActiveState=inactive\n")}, nil
		}
		return sysexec.Result{}, nil
	})
	run.Handle("btrfs", func([]string) (sysexec.Result, error) { return sysexec.Result{}, nil })
	run.Handle("systemd-run", func([]string) (sysexec.Result, error) { return sysexec.Result{}, nil })
	run.Handle("docker", func(args []string) (sysexec.Result, error) {
		if slices.Contains(args, "config") {
			return sysexec.Result{Stdout: []byte(config)}, nil
		}
		return sysexec.Result{}, nil
	})
	return &Ops{Run: run, Files: files, Logger: quietLogger()}, run
}

func dockerCalls(run *sysexec.Fake) []string {
	var out []string
	for _, c := range run.Calls() {
		if c[0] == "docker" || c[0] == "systemd-run" {
			out = append(out, strings.Join(c, " "))
		}
	}
	return out
}

// TestDeployCreatesBindSources: the catalog forbids Docker to create an
// application's folders (create_host_path: false), so the deployment creates
// the missing ones, inside the application's folder only (ADR-0036).
func TestDeployCreatesBindSources(t *testing.T) {
	ctx := context.Background()
	config := `{"name":"cwnsb-portainer","services":{"portainer":{"image":"portainer/portainer-ce:lts","volumes":[` +
		`{"type":"bind","source":"/var/run/docker.sock","target":"/var/run/docker.sock","bind":{}},` +
		`{"type":"bind","source":"/srv/cwnsb/tank/.apps/portainer/data","target":"/data","bind":{}},` +
		`{"type":"bind","source":"/srv/cwnsb/tank/.apps/portainer/conf/tls","target":"/tls","bind":{}},` +
		`{"type":"bind","source":"/srv/cwnsb/tank/media","target":"/media","bind":{}},` +
		`{"type":"volume","source":"cache","target":"/cache","volume":{}}]}}}`
	files := NewMemFiles()
	o, run := newDockerOps(files, config)
	if _, err := o.appDeploy(ctx, AppDeployArgs{Pool: "tank", Name: "portainer", Compose: "services:\n  portainer: {}\n"}); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"/srv/cwnsb/tank/.apps/portainer/data", "/srv/cwnsb/tank/.apps/portainer/conf/tls"} {
		if !files.Exists(dir) {
			t.Errorf("%s not created", dir)
		}
	}
	for _, other := range []string{"/var/run/docker.sock", "/srv/cwnsb/tank/media", "/srv/cwnsb/tank/.apps/portainer/cache"} {
		if files.Exists(other) {
			t.Errorf("%s created: not the application's folder", other)
		}
	}
	calls := dockerCalls(run)
	if len(calls) != 2 || !strings.HasSuffix(calls[0], "/srv/cwnsb/tank/.apps/portainer/.compose.new.yaml config --format json") ||
		!strings.Contains(calls[1], "/usr/bin/docker compose -p cwnsb-portainer -f /srv/cwnsb/tank/.apps/portainer/compose.yaml up -d") {
		t.Fatalf("calls = %q", calls)
	}

	// Not the JSON Compose prints: refused, and the new folder is undone.
	files = NewMemFiles()
	o, _ = newDockerOps(files, "services: {}\n")
	if _, err := o.appDeploy(ctx, AppDeployArgs{Pool: "tank", Name: "portainer", Compose: "services:\n  portainer: {}\n"}); err == nil || err.Code != CodeFailed {
		t.Fatalf("unreadable config: %v", err)
	}
	if files.Exists("/srv/cwnsb/tank/.apps/portainer/compose.yaml") {
		t.Fatal("refused deployment left its compose file")
	}
}

// TestAppOrphan: the containers of a project without a recorded application
// are stopped or removed by the project's name alone (ADR-0036).
func TestAppOrphan(t *testing.T) {
	ctx := context.Background()
	o, run := newDockerOps(NewMemFiles(), "")
	if _, err := o.appOrphan(ctx, OrphanArgs{Name: "uptime-kuma", Action: "stop"}); err != nil {
		t.Fatal(err)
	}
	if _, err := o.appOrphan(ctx, OrphanArgs{Name: "uptime-kuma", Action: "remove"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"docker compose -p cwnsb-uptime-kuma stop", "docker compose -p cwnsb-uptime-kuma down --remove-orphans"}
	if got := dockerCalls(run); !slices.Equal(got, want) {
		t.Fatalf("calls = %q", got)
	}
	for _, bad := range []OrphanArgs{{Name: "../x", Action: "stop"}, {Name: "Kuma", Action: "remove"}, {Name: "kuma", Action: "rm -v"}, {Name: "kuma"}} {
		if _, err := o.appOrphan(ctx, bad); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
	if got := dockerCalls(run); len(got) != 2 {
		t.Fatalf("refused calls ran docker: %q", got)
	}
}

// TestEngineDataPools: Docker not installed (a new SD card), its data still
// on tank: the status names that pool and its containers, never a pool
// without a Docker folder (P9).
func TestEngineDataPools(t *testing.T) {
	ctx := context.Background()
	files := NewMemFiles()
	files.MkdirAll("/srv/cwnsb/e2", 0o755)
	files.MkdirAll("/srv/cwnsb/tank/.docker/containers/8ec91cc0976e", 0o710)
	files.MkdirAll("/srv/cwnsb/tank/.docker/containers/a9e0c220e5be", 0o710)
	files.MkdirAll("/srv/cwnsb/old/.docker", 0o710) // Docker never started there
	files.MkdirAll("/srv/cwnsb/Bad/.docker/containers/x", 0o710)
	run := sysexec.NewFake()
	run.Handle("dpkg-query", func([]string) (sysexec.Result, error) {
		return sysexec.Result{ExitCode: 1, Stderr: []byte("dpkg-query: no packages found matching docker.io\n")}, nil
	})
	run.Handle("systemctl", func(args []string) (sysexec.Result, error) {
		if args[0] == "is-active" {
			return sysexec.Result{ExitCode: 4, Stdout: []byte("inactive\n")}, nil
		}
		return sysexec.Result{Stdout: []byte("LoadState=not-found\n")}, nil
	})
	o := &Ops{Run: run, Files: files, Logger: quietLogger()}
	st := o.engineStatus(ctx)
	want := []EngineData{{Pool: "old"}, {Pool: "tank", Containers: 2}}
	if st.Installed || !slices.Equal(st.DataPools, want) {
		t.Fatalf("status = %+v", st)
	}

	// Installed: Docker's own pool says where its data is.
	o, _ = newDockerOps(files, "")
	if st := o.engineStatus(ctx); !st.Installed || st.DataPools == nil || len(st.DataPools) != 0 {
		t.Fatalf("installed = %+v", st)
	}
}
