// SPDX-License-Identifier: AGPL-3.0-or-later

// Package apps holds the pure logic of applications (ADR-0013): names and
// paths, Docker's configuration, Compose's JSON outputs, transient-unit job
// states, and the built-in catalog.
package apps

import (
	"bufio"
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/crosswax/nasbuster/internal/storage"
)

// Fixed paths.
const (
	DaemonJSON  = "/etc/docker/daemon.json"
	DropInName  = "docker.service.d/cwnsb.conf" // under the systemd unit directory
	ComposeFile = "compose.yaml"
	EngineJob   = "engine"
	LocateJob   = "locate" // a disk read continuously so that its LED blinks
	// InitramfsJob rebuilds the initramfs after mdadm.conf changed (ADR-0033).
	InitramfsJob = "initramfs"
	// MaxCompose bounds a compose file (bytes).
	MaxCompose = 64 << 10
)

// Packages installed for the engine (docker-cli is only "recommended" by docker.io).
var Packages = []string{"docker.io", "docker-cli", "docker-compose"}

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,29}$`)

// ValidName checks an application name (also a Compose project suffix).
func ValidName(name string) bool { return nameRe.MatchString(name) && !strings.HasSuffix(name, "-") }

// Project is the Compose project name of an application.
func Project(name string) string { return "cwnsb-" + name }

// EngineDir is Docker's data-root on a pool.
func EngineDir(pool string) string { return storage.MountPoint(pool) + "/.docker" }

// AppsDir holds the applications of a pool.
func AppsDir(pool string) string { return storage.MountPoint(pool) + "/.apps" }

// AppDir is one application's directory (a subvolume).
func AppDir(pool, name string) string { return AppsDir(pool) + "/" + name }

// ComposePath is an application's compose file.
func ComposePath(pool, name string) string { return AppDir(pool, name) + "/" + ComposeFile }

// ProjectPool reads the pool of an application from the compose file Docker
// remembers for its project: "" unless it is where NasBuster puts it.
func ProjectPool(name, configFiles string) string {
	rest, ok := strings.CutPrefix(configFiles, storage.MountRoot+"/")
	pool, tail, _ := strings.Cut(rest, "/")
	if !ok || !storage.ValidPoolName(pool) || tail != ".apps/"+name+"/"+ComposeFile {
		return ""
	}
	return pool
}

// BindSources lists the host paths bound into the containers, read from
// `docker compose config --format json`: absolute, relative ones resolved
// from the compose file's folder.
func BindSources(configJSON []byte) ([]string, error) {
	var c struct {
		Services map[string]struct {
			Volumes []struct {
				Type   string `json:"type"`
				Source string `json:"source"`
			} `json:"volumes"`
		} `json:"services"`
	}
	if err := json.Unmarshal(configJSON, &c); err != nil {
		return nil, fmt.Errorf("compose config: %w", err)
	}
	seen := map[string]bool{}
	out := []string{}
	for _, s := range c.Services {
		for _, v := range s.Volumes {
			if v.Type == "bind" && v.Source != "" && !seen[v.Source] {
				seen[v.Source] = true
				out = append(out, v.Source)
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// InAppDir returns source relative to an application's folder when it lies
// strictly inside it.
func InAppDir(pool, name, source string) (string, bool) {
	rel, ok := strings.CutPrefix(path.Clean(source), AppDir(pool, name)+"/")
	if !ok || rel == "" {
		return "", false
	}
	return rel, true
}

// TrashDays is how long the data of a removed application is kept (ADR-0035).
const TrashDays = 7

// TrashDir keeps read-only snapshots of removed applications' data, in the
// pool's root-only folder (ADR-0035).
func TrashDir(pool string) string { return storage.MountPoint(pool) + "/.cwnsb/trash" }

const trashTime = "20060102T150405Z"

// TrashEntry names the snapshot of an application's data removed at t.
func TrashEntry(name string, t time.Time) string {
	return "app-" + name + "-" + t.UTC().Format(trashTime)
}

// ParseTrashEntry reads an entry of TrashDir back: the application and when
// its data was removed.
func ParseTrashEntry(entry string) (name string, at time.Time, ok bool) {
	rest, found := strings.CutPrefix(entry, "app-")
	i := strings.LastIndex(rest, "-")
	if !found || i < 1 {
		return "", time.Time{}, false
	}
	at, err := time.Parse(trashTime, rest[i+1:])
	if err != nil || !ValidName(rest[:i]) {
		return "", time.Time{}, false
	}
	return rest[:i], at, true
}

// Long Btrfs operations on a pool, one transient unit each (ADR-0026).
const (
	ScrubJob   = "scrub"
	BalanceJob = "balance"
	ReplaceJob = "replace"
	ResyncJob  = "resync" // balance, then scrub, after a disk came back (ADR-0027)
)

// PoolJob names the job of a long operation on a pool ("scrub-tank").
func PoolJob(kind, pool string) string { return kind + "-" + pool }

// JobUnit is the transient unit running a job ("engine" or "app-<name>").
func JobUnit(job string) string { return "cwnsb-job-" + job + ".service" }

// ValidJob checks a job name.
func ValidJob(job string) bool {
	if job == EngineJob || job == LocateJob {
		return true
	}
	if share, ok := strings.CutPrefix(job, "import-"); ok {
		return validShareName(share)
	}
	for _, kind := range []string{ScrubJob, BalanceJob, ReplaceJob, ResyncJob} {
		if pool, ok := strings.CutPrefix(job, kind+"-"); ok {
			return storage.ValidPoolName(pool)
		}
	}
	name, ok := strings.CutPrefix(job, "app-")
	return ok && ValidName(name)
}

// DaemonConfig renders /etc/docker/daemon.json: data on the pool, bounded logs.
// No live-restore (ADR-0029): with it, Docker leaves the containers running
// when it stops, systemd never stops them (runc's scopes have no default
// dependencies), and every shutdown of the Pi waited 90 s for them; a pool
// holding Docker's data could not be unmounted either. Written out as false
// so that a reload of Docker applies it to an existing installation.
func DaemonConfig(pool string) []byte {
	b, _ := json.MarshalIndent(map[string]any{
		"data-root":    EngineDir(pool),
		"log-driver":   "local",
		"log-opts":     map[string]string{"max-size": "10m", "max-file": "3"},
		"live-restore": false,
	}, "", "  ")
	return append(b, '\n')
}

// WithoutLiveRestore turns live-restore off in an existing daemon.json, keeping
// everything else (the user may have added registry mirrors). ok is false when
// there is nothing to change or the file is not valid JSON.
func WithoutLiveRestore(daemonJSON []byte) (out []byte, ok bool) {
	var c map[string]any
	if json.Unmarshal(daemonJSON, &c) != nil || c["live-restore"] != true {
		return nil, false
	}
	c["live-restore"] = false
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return nil, false
	}
	return append(b, '\n'), true
}

// DropIn makes docker.service wait for the pool: without it, Docker does not
// start and never writes to the SD card.
func DropIn(pool string) string {
	return fmt.Sprintf("# Managed by CrossWax NasBuster — do not edit.\n[Unit]\nRequiresMountsFor=%s\n", storage.MountPoint(pool))
}

// EnginePool reads the pool name from daemon.json ("" if not ours).
func EnginePool(daemonJSON []byte) string {
	var c struct {
		DataRoot string `json:"data-root"`
	}
	if json.Unmarshal(daemonJSON, &c) != nil {
		return ""
	}
	rest, ok := strings.CutPrefix(c.DataRoot, storage.MountRoot+"/")
	if !ok {
		return ""
	}
	pool, tail, _ := strings.Cut(rest, "/")
	if tail != ".docker" || !storage.ValidPoolName(pool) {
		return ""
	}
	return pool
}

// Job is the state of a transient unit.
type Job struct {
	State      string   `json:"state"` // none, running, succeeded, failed
	ExitStatus int      `json:"exitStatus"`
	Log        []string `json:"log"`
}

// ParseJob reads `systemctl show -p LoadState,ActiveState,Result,ExecMainStatus`.
func ParseJob(text string) Job {
	kv := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			kv[k] = v
		}
	}
	j := Job{State: "none", Log: []string{}}
	j.ExitStatus, _ = strconv.Atoi(kv["ExecMainStatus"])
	switch {
	case kv["LoadState"] == "not-found" || kv["ActiveState"] == "":
	case kv["ActiveState"] == "activating" || kv["ActiveState"] == "deactivating" || kv["ActiveState"] == "reloading":
		j.State = "running"
	case kv["ActiveState"] == "failed" || (kv["Result"] != "" && kv["Result"] != "success"):
		j.State = "failed"
	case kv["ActiveState"] == "active":
		j.State = "succeeded"
	}
	return j
}

// ProjectStatus is one line of `docker compose ls --all --format json`.
type ProjectStatus struct {
	Name        string `json:"name"`
	Status      string `json:"status"` // "running(2)", "exited(1)", "running(1), exited(1)"
	ConfigFiles string `json:"configFiles"`
}

// ParseProjects reads `docker compose ls --all --format json`.
func ParseProjects(data []byte) ([]ProjectStatus, error) {
	var raw []struct {
		Name, Status, ConfigFiles string
	}
	if err := json.Unmarshal(bytes.TrimSpace(data), &raw); err != nil {
		return nil, fmt.Errorf("compose ls: %w", err)
	}
	out := make([]ProjectStatus, 0, len(raw))
	for _, p := range raw {
		out = append(out, ProjectStatus{Name: p.Name, Status: p.Status, ConfigFiles: p.ConfigFiles})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Port is a published port.
type Port struct {
	URL           string `json:"url"`
	TargetPort    int    `json:"targetPort"`
	PublishedPort int    `json:"publishedPort"`
	Protocol      string `json:"protocol"`
}

// Container is one line of `docker compose ps --all --format json`.
type Container struct {
	Name    string `json:"name"`
	Service string `json:"service"`
	Image   string `json:"image"`
	State   string `json:"state"`  // running, exited, restarting, created, paused, dead
	Health  string `json:"health"` // "", healthy, unhealthy, starting
	Status  string `json:"status"` // "Up 2 seconds"
	Ports   []Port `json:"ports"`
}

// ParseContainers reads `docker compose ps --all --format json`: one JSON
// object per line in Compose 2.26 (older versions printed an array).
func ParseContainers(data []byte) ([]Container, error) {
	type raw struct {
		Name, Service, Image, State, Health, Status string
		Publishers                                  []struct {
			URL           string
			TargetPort    int
			PublishedPort int
			Protocol      string
		}
	}
	var rows []raw
	trimmed := bytes.TrimSpace(data)
	if bytes.HasPrefix(trimmed, []byte("[")) {
		if err := json.Unmarshal(trimmed, &rows); err != nil {
			return nil, fmt.Errorf("compose ps: %w", err)
		}
	} else {
		sc := bufio.NewScanner(bytes.NewReader(trimmed))
		sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
		for sc.Scan() {
			line := bytes.TrimSpace(sc.Bytes())
			if len(line) == 0 {
				continue
			}
			var r raw
			if err := json.Unmarshal(line, &r); err != nil {
				return nil, fmt.Errorf("compose ps: %w", err)
			}
			rows = append(rows, r)
		}
	}
	out := make([]Container, 0, len(rows))
	for _, r := range rows {
		c := Container{Name: r.Name, Service: r.Service, Image: r.Image, State: r.State, Health: r.Health, Status: r.Status, Ports: []Port{}}
		seen := map[string]bool{}
		for _, p := range r.Publishers {
			key := fmt.Sprintf("%d/%d/%s", p.PublishedPort, p.TargetPort, p.Protocol)
			if p.PublishedPort == 0 || seen[key] { // IPv4 and IPv6 bindings of one port
				continue
			}
			seen[key] = true
			c.Ports = append(c.Ports, Port{URL: p.URL, TargetPort: p.TargetPort, PublishedPort: p.PublishedPort, Protocol: p.Protocol})
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Template is a catalog entry.
type Template struct {
	Name        string            `json:"name"`
	Title       string            `json:"title"`
	Description map[string]string `json:"description"` // en, fr
	Expert      bool              `json:"expert"`      // full control of Docker
	WebPort     int               `json:"webPort"`
	WebScheme   string            `json:"webScheme"`
	SetupToken  bool              `json:"setupToken"` // prints a one-time setup token (Portainer, setuptoken.go)
	Compose     string            `json:"compose"`
}

//go:embed catalog
var catalogFS embed.FS

// Catalog returns the built-in templates, sorted by name.
func Catalog() []Template {
	entries, _ := fs.ReadDir(catalogFS, "catalog")
	var out []Template
	for _, e := range entries {
		if path.Ext(e.Name()) != ".json" {
			continue
		}
		b, _ := fs.ReadFile(catalogFS, "catalog/"+e.Name())
		var t Template
		if json.Unmarshal(b, &t) != nil {
			continue
		}
		compose, err := fs.ReadFile(catalogFS, "catalog/"+strings.TrimSuffix(e.Name(), ".json")+".yaml")
		if err != nil {
			continue
		}
		t.Compose = string(compose)
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// TemplateByName finds a catalog entry.
func TemplateByName(name string) (Template, bool) {
	for _, t := range Catalog() {
		if t.Name == name {
			return t, true
		}
	}
	return Template{}, false
}

var shareNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,19}$`)

// validShareName mirrors shares.ValidShareName (no import cycle).
func validShareName(s string) bool { return shareNameRe.MatchString(s) }
