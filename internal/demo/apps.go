// SPDX-License-Identifier: AGPL-3.0-or-later

package demo

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/crosswax/nasbuster/internal/apps"
	"github.com/crosswax/nasbuster/internal/sysexec"
)

// appsSim emulates Docker, Compose and the transient units the helper uses
// for long jobs (ADR-0013), with the formats seen in the lab.
type appsSim struct {
	sys *System

	mu        sync.Mutex
	installed bool
	jobs      map[string]*simJob     // unit name (cwnsb-job-…) -> job
	projects  map[string]*simProject // compose project -> state
	// dormant: the projects left in Docker's data folder of a pool by an
	// uninstallation that kept the data (pool -> project -> state).
	dormant map[string]map[string]*simProject
	// btrfs runs the long pool operations started in units (ADR-0026): the
	// pool simulation keeps their progress.
	btrfs func([]string) (sysexec.Result, error)
	// mdadm runs the parity repair after a reassembly (ADR-0032).
	mdadm func([]string) (sysexec.Result, error)
}

type simJob struct {
	started  time.Time
	length   time.Duration
	done     func() // applied once when the job ends
	ended    bool
	log      []string
	desc     string
	progress func(frac float64) // optional, called on every tick
}

type simProject struct {
	file     string
	running  bool
	started  time.Time // last start of its containers
	services []simService
}

type simService struct {
	name, image string
	ports       []int
	binds       []string // host paths as written ("./data", "/var/run/docker.sock")
}

// Demo durations.
const (
	simInstallTime   = 20 * time.Second
	simDeployTime    = 15 * time.Second
	simInitramfsTime = 30 * time.Second
)

// initramfsTool rebuilds the initramfs on Raspberry Pi OS: on -u, every
// flavour of the latest kernel (ADR-0033).
const initramfsTool = "/usr/sbin/update-initramfs"

// initramfsLog is what the rebuild wrote on the Pi 5 (13 s, 2026-10-27).
var initramfsLog = []string{
	"update-initramfs: Generating /boot/initrd.img-6.18.50+rpt-rpi-v8",
	"'/boot/initrd.img-6.18.50+rpt-rpi-v8' -> '/boot/firmware/initramfs8'",
	"update-initramfs: Generating /boot/initrd.img-6.18.50+rpt-rpi-2712",
	"'/boot/initrd.img-6.18.50+rpt-rpi-2712' -> '/boot/firmware/initramfs_2712'",
}

func newAppsSim(s *System) *appsSim {
	return &appsSim{sys: s, jobs: map[string]*simJob{}, projects: map[string]*simProject{}, dormant: map[string]map[string]*simProject{}}
}

func (a *appsSim) register(f *sysexec.Fake, poolSystemctl, poolBtrfs, poolMdadm func([]string) (sysexec.Result, error)) {
	a.btrfs, a.mdadm = poolBtrfs, poolMdadm
	f.Handle("dpkg-query", a.dpkgQuery)
	f.Handle("systemd-run", a.systemdRun)
	f.Handle("journalctl", a.journalctl)
	f.Handle("docker", a.docker)
	f.Handle("apt-get", a.aptGet)
	f.Handle("systemctl", func(args []string) (sysexec.Result, error) {
		if res, ok := a.systemctl(args); ok {
			return res, nil
		}
		return poolSystemctl(args)
	})
}

// tick ends the jobs whose time has come.
func (a *appsSim) tick() {
	now := a.sys.Clock()
	for _, j := range a.jobs {
		if !j.ended && now.Sub(j.started) >= j.length {
			j.ended = true
			if j.done != nil {
				j.done()
			}
		} else if !j.ended && j.progress != nil {
			j.progress(now.Sub(j.started).Seconds() / j.length.Seconds())
		}
	}
}

func (a *appsSim) dpkgQuery(args []string) (sysexec.Result, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tick()
	if !a.installed {
		return sysexec.Result{ExitCode: 1, Stderr: []byte("dpkg-query: no packages found matching docker.io\n")}, nil
	}
	return sysexec.Result{Stdout: []byte("docker.io installed 26.1.5+dfsg1-9+deb13u1\ndocker-cli installed 26.1.5+dfsg1-9+deb13u1\ndocker-compose installed 2.26.1-4\n")}, nil
}

func (a *appsSim) systemctl(args []string) (sysexec.Result, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tick()
	switch {
	case len(args) == 2 && args[0] == "is-active" && args[1] == "docker.service":
		if a.installed {
			return sysexec.Result{Stdout: []byte("active\n")}, true
		}
		return sysexec.Result{ExitCode: 3, Stdout: []byte("inactive\n")}, true
	case len(args) == 5 && args[0] == "show" && args[3] == "Description":
		if j, ok := a.jobs[args[1]]; ok {
			return sysexec.Result{Stdout: []byte(j.desc + "\n")}, true
		}
		return sysexec.Result{Stdout: []byte("\n")}, true
	case len(args) >= 2 && args[0] == "list-units":
		var lines []string
		for unit, j := range a.jobs {
			if strings.HasPrefix(unit, "cwnsb-job-import-") && !j.ended {
				lines = append(lines, unit+" loaded activating start "+j.desc)
			}
		}
		return sysexec.Result{Stdout: []byte(strings.Join(lines, "\n") + "\n")}, true
	case len(args) >= 2 && args[0] == "show" && strings.HasPrefix(args[1], "cwnsb-job-"):
		j, ok := a.jobs[args[1]]
		switch {
		case !ok:
			return sysexec.Result{Stdout: []byte("LoadState=not-found\nActiveState=inactive\nResult=success\nExecMainStatus=0\n")}, true
		case !j.ended:
			return sysexec.Result{Stdout: []byte("LoadState=loaded\nActiveState=activating\nResult=success\nExecMainStatus=0\n")}, true
		default:
			return sysexec.Result{Stdout: []byte("LoadState=loaded\nActiveState=active\nResult=success\nExecMainStatus=0\n")}, true
		}
	case len(args) == 2 && (args[0] == "stop" || args[0] == "reset-failed") && strings.HasPrefix(args[1], "cwnsb-job-"):
		if j, ok := a.jobs[args[1]]; ok && (args[0] == "stop" || j.ended) {
			delete(a.jobs, args[1])
		}
		return done, true
	}
	return sysexec.Result{}, false
}

// systemdRun starts a transient unit: the engine installation or a deployment.
func (a *appsSim) systemdRun(args []string) (sysexec.Result, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	unit, desc := "", ""
	var cmd, pre, post []string
	for i, arg := range args {
		if v, ok := strings.CutPrefix(arg, "--unit="); ok {
			unit = v + ".service"
		}
		if v, ok := strings.CutPrefix(arg, "--description="); ok {
			desc = v
		}
		if v, ok := strings.CutPrefix(arg, "ExecStartPre="); ok {
			pre = append(pre, v)
		}
		if v, ok := strings.CutPrefix(arg, "ExecStartPost="); ok {
			post = append(post, v)
		}
		if strings.HasPrefix(arg, "/usr/bin/") || strings.HasPrefix(arg, "/usr/sbin/") {
			cmd = args[i:]
			break
		}
	}
	if unit == "" || len(cmd) == 0 {
		return fail("unsupported systemd-run call")
	}
	now := a.sys.Clock()
	defer func() {
		if j := a.jobs[unit]; j != nil {
			j.desc = desc
		}
	}()
	switch {
	case cmd[0] == "/usr/bin/btrfs" && a.btrfs != nil:
		for _, line := range pre {
			if f := strings.Fields(line); len(f) > 1 && f[0] == "/usr/bin/btrfs" {
				if res, err := a.btrfs(f[1:]); err != nil || res.ExitCode != 0 {
					return res, err
				}
			}
		}
		if res, err := a.btrfs(cmd[1:]); err != nil || res.ExitCode != 0 {
			return res, err
		}
		// The pool simulation carries the operation's progress: the unit itself ends at once.
		a.jobs[unit] = &simJob{started: now}
	case cmd[0] == "/usr/sbin/mdadm" && a.mdadm != nil:
		// Like `mdadm --action=repair`: md runs the repair after the unit ends.
		if res, err := a.mdadm(cmd[1:]); err != nil || res.ExitCode != 0 {
			return res, err
		}
		a.jobs[unit] = &simJob{started: now}
	case cmd[0] == initramfsTool && len(cmd) == 2 && cmd[1] == "-u":
		a.jobs[unit] = &simJob{started: now, length: simInitramfsTime, log: initramfsLog, done: func() { a.moveFiles(post) }}
	case cmd[0] == "/usr/bin/timeout":
		a.jobs[unit] = &simJob{started: now, length: 30 * time.Second}
	case cmd[0] == "/usr/bin/rsync":
		a.jobs[unit] = a.importJob(cmd, now)
	case cmd[0] == "/usr/bin/apt-get":
		a.jobs[unit] = &simJob{started: now, length: simInstallTime, done: a.engineInstalled,
			log: []string{"Reading package lists...", "Setting up containerd (1.7.24~ds1-6+deb13u1) ...", "Setting up docker.io (26.1.5+dfsg1-9+deb13u1) ...",
				"Created symlink '/etc/systemd/system/multi-user.target.wants/docker.service'.", "Setting up docker-compose (2.26.1-4) ..."}}
	case cmd[0] == "/usr/bin/docker" && len(cmd) >= 6 && cmd[1] == "compose":
		project, file := cmd[3], cmd[5]
		services := a.parseServices(file)
		a.jobs[unit] = &simJob{started: now, length: simDeployTime, done: func() {
			a.projects[project] = &simProject{file: file, running: true, started: a.sys.Clock(), services: services}
			a.writeContainers(project)
		}, log: []string{"Pulling images (demo)...", " Network " + project + "_default  Created", " Container " + project + "-1  Started"}}
	default:
		return fail("unsupported systemd-run command")
	}
	return sysexec.Result{Stderr: []byte("Running as unit: " + unit + "\n")}, nil
}

func (a *appsSim) journalctl(args []string) (sysexec.Result, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tick()
	if len(args) == 1 && args[0] == "--flush" { // the persistent journal (ADR-0031)
		return done, nil
	}
	if len(args) < 2 || args[0] != "-u" {
		return fail("unsupported journalctl call")
	}
	j, ok := a.jobs[args[1]]
	if !ok {
		return sysexec.Result{Stdout: []byte("-- No entries --\n")}, nil
	}
	// The log grows with the job's progress.
	n := len(j.log)
	if !j.ended {
		n = 1 + int(float64(len(j.log)-1)*a.sys.Clock().Sub(j.started).Seconds()/j.length.Seconds())
	}
	return sysexec.Result{Stdout: []byte(strings.Join(j.log[:min(n, len(j.log))], "\n") + "\n")}, nil
}

// moveFiles runs the "/usr/bin/mv -f <from> <to>" lines a unit runs after a
// success (the initramfs rebuild records the mdadm.conf it was built with).
func (a *appsSim) moveFiles(lines []string) {
	for _, line := range lines {
		f := strings.Fields(line)
		if len(f) != 4 || f[0] != "/usr/bin/mv" || f[1] != "-f" {
			continue
		}
		if b, err := a.sys.Files.ReadFile(f[2]); err == nil {
			_ = a.sys.Files.WriteFile(f[3], b, 0o600)
			_ = a.sys.Files.Remove(f[2])
		}
	}
}

// parseServices reads service names, images and published ports with a
// naive line scan: enough for the demo, never used outside it.
func (a *appsSim) parseServices(file string) []simService {
	b, err := a.sys.Files.ReadFile(file)
	if err != nil {
		return nil
	}
	var out []simService
	inServices := false
	for _, line := range strings.Split(string(b), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case line == "services:":
			inServices = true
		case inServices && strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   ") && strings.HasSuffix(trimmed, ":"):
			out = append(out, simService{name: strings.TrimSuffix(trimmed, ":")})
		case len(out) > 0 && strings.HasPrefix(trimmed, "image:"):
			out[len(out)-1].image = strings.TrimSpace(strings.TrimPrefix(trimmed, "image:"))
		case len(out) > 0 && strings.HasPrefix(trimmed, "source:"):
			out[len(out)-1].binds = append(out[len(out)-1].binds, strings.TrimSpace(strings.TrimPrefix(trimmed, "source:")))
		case len(out) > 0 && (strings.HasPrefix(trimmed, "- ./") || strings.HasPrefix(trimmed, "- /")) && strings.Contains(trimmed, ":"):
			host, _, _ := strings.Cut(strings.TrimPrefix(trimmed, "- "), ":")
			out[len(out)-1].binds = append(out[len(out)-1].binds, host)
		case len(out) > 0 && strings.HasPrefix(trimmed, "- \"") && strings.Contains(trimmed, ":"):
			var host int
			fmt.Sscanf(strings.TrimPrefix(trimmed, "- \""), "%d", &host)
			if host > 0 {
				out[len(out)-1].ports = append(out[len(out)-1].ports, host)
			}
		case line != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "#"):
			inServices = line == "services:"
		}
	}
	return out
}

// composeConfig prints what `docker compose config --format json` prints
// (Compose 2.26.1): bind sources absolute, relative ones resolved from the
// compose file's folder.
func composeConfig(project, file string, services []simService) []byte {
	svcs := map[string]any{}
	for _, s := range services {
		vols := []map[string]any{}
		for _, src := range s.binds {
			if !path.IsAbs(src) {
				src = path.Join(path.Dir(file), src)
			}
			vols = append(vols, map[string]any{"type": "bind", "source": src, "target": "/data", "bind": map[string]any{}})
		}
		svcs[s.name] = map[string]any{"image": s.image, "volumes": vols}
	}
	b, _ := json.MarshalIndent(map[string]any{"name": project, "services": svcs}, "", "  ")
	return b
}

func (a *appsSim) docker(args []string) (sysexec.Result, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tick()
	if !a.installed {
		return sysexec.Result{ExitCode: 127, Stderr: []byte("docker: not installed\n")}, nil
	}
	if len(args) == 3 && args[0] == "ps" && args[1] == "--all" && args[2] == "--quiet" {
		var ids []string
		for n, p := range a.projects {
			for _, s := range p.services {
				ids = append(ids, fmt.Sprintf("%x", hash32(n+s.name))[:8])
			}
		}
		return sysexec.Result{Stdout: []byte(strings.Join(ids, "\n") + "\n")}, nil
	}
	if len(args) < 2 || args[0] != "compose" {
		return fail("unsupported docker call")
	}
	args = args[1:]
	if args[0] == "ls" {
		var out []map[string]string
		names := make([]string, 0, len(a.projects))
		for n := range a.projects {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			p := a.projects[n]
			status := fmt.Sprintf("running(%d)", len(p.services))
			if !p.running {
				status = fmt.Sprintf("exited(%d)", len(p.services))
			}
			out = append(out, map[string]string{"Name": n, "Status": status, "ConfigFiles": p.file})
		}
		if out == nil {
			return sysexec.Result{Stdout: []byte("[]\n")}, nil
		}
		b, _ := json.Marshal(out)
		return sysexec.Result{Stdout: b}, nil
	}
	if len(args) < 3 || args[0] != "-p" {
		return fail("unsupported compose call")
	}
	project := args[1]
	file := ""
	rest := args[2:]
	if len(rest) >= 2 && rest[0] == "-f" {
		file, rest = rest[1], rest[2:]
	}
	if len(rest) == 0 {
		return fail("missing compose command")
	}
	p := a.projects[project]
	switch rest[0] {
	case "config":
		b, err := a.sys.Files.ReadFile(file)
		if err != nil || !strings.Contains(string(b), "services:") {
			return sysexec.Result{ExitCode: 15, Stderr: []byte("yaml: line 1: did not find expected key\n")}, nil
		}
		return sysexec.Result{Stdout: composeConfig(project, file, a.parseServices(file))}, nil
	case "ps":
		var lines []string
		if p != nil {
			for _, s := range p.services {
				state, status := "running", "Up 3 minutes"
				if !p.running {
					state, status = "exited", "Exited (0) 1 minute ago"
				}
				var pubs []map[string]any
				for _, port := range s.ports {
					pubs = append(pubs, map[string]any{"URL": "0.0.0.0", "TargetPort": port, "PublishedPort": port, "Protocol": "tcp"})
				}
				b, _ := json.Marshal(map[string]any{"Name": project + "-" + s.name + "-1", "Service": s.name, "Image": s.image,
					"State": state, "Health": "", "Status": status, "Publishers": pubs})
				lines = append(lines, string(b))
			}
		}
		return sysexec.Result{Stdout: []byte(strings.Join(lines, "\n") + "\n")}, nil
	case "start", "restart":
		if p != nil {
			if rest[0] == "restart" || !p.running {
				p.started = a.sys.Clock()
			}
			p.running = true
		}
		return done, nil
	case "stop":
		if p != nil {
			p.running = false
		}
		return done, nil
	case "down":
		a.removeContainers(project)
		delete(a.projects, project)
		return done, nil
	case "logs":
		if p == nil {
			return done, nil
		}
		var b strings.Builder
		for _, s := range p.services {
			if strings.HasPrefix(s.image, "portainer/portainer") {
				b.WriteString(portainerLogs(s.name, p.started, a.sys.Clock()))
				continue
			}
			fmt.Fprintf(&b, "%s-1  | %s demo: %s started\n", s.name, a.sys.Clock().UTC().Format(time.RFC3339), s.name)
		}
		return sysexec.Result{Stdout: []byte(b.String())}, nil
	}
	return fail("unsupported compose command " + rest[0])
}

// aptGet handles the engine's purge.
func (a *appsSim) aptGet(args []string) (sysexec.Result, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(args) > 0 && args[0] == "purge" {
		a.installed = false
		// The containers stay in Docker's data folder on the pool (deleted
		// only with it): a reinstallation there takes them back (P9).
		if pool := a.enginePool(); pool != "" && len(a.projects) > 0 {
			a.dormant[pool] = a.projects
		}
		a.projects = map[string]*simProject{}
		return done, nil
	}
	return fail("unsupported apt-get call")
}

// enginePool is the pool of Docker's data, from the daemon.json the helper wrote.
func (a *appsSim) enginePool() string {
	b, err := a.sys.Files.ReadFile(apps.DaemonJSON)
	if err != nil {
		return ""
	}
	return apps.EnginePool(b)
}

// containerDir is where Docker keeps a container, in its data folder.
func (a *appsSim) containerDir(project string, s simService) string {
	return apps.EngineDir(a.enginePool()) + "/containers/" + fmt.Sprintf("%x", hash32(project+s.name))[:8]
}

func (a *appsSim) writeContainers(project string) {
	if a.enginePool() == "" {
		return
	}
	for _, s := range a.projects[project].services {
		_ = a.sys.Files.WriteFile(a.containerDir(project, s)+"/config.v2.json", []byte("{}"), 0o600)
	}
}

func (a *appsSim) removeContainers(project string) {
	p := a.projects[project]
	if p == nil || a.enginePool() == "" {
		return
	}
	for _, s := range p.services {
		a.sys.Files.RemoveAll(a.containerDir(project, s))
	}
}

// engineInstalled ends the installation. Like Docker on a data folder kept
// from before, the containers still in it start again by themselves
// (restart: unless-stopped), with or without an application recorded.
func (a *appsSim) engineInstalled() {
	a.installed = true
	pool := a.enginePool()
	back := map[string]*simProject{}
	for name, p := range a.dormant[pool] {
		if len(p.services) > 0 && a.sys.Files.Exists(a.containerDir(name, p.services[0])+"/config.v2.json") {
			p.running, p.started = true, a.sys.Clock()
			back[name] = p
		}
	}
	delete(a.dormant, pool)
	if len(back) > 0 {
		a.projects = back
	}
}

// portainerLogs imitates Portainer 2.45's start (a new setup token every
// time) and its lockout 5 minutes later, as `docker compose logs --no-color
// --timestamps` prints them (internal/apps/testdata/portainer-2.45.2.log).
func portainerLogs(service string, started, now time.Time) string {
	sum := sha256.Sum256([]byte("cwnsb-demo-portainer " + started.UTC().Format(time.RFC3339Nano)))
	line := func(at time.Time, text string) string {
		return "\x1b[2K" + service + "-1  | " + at.UTC().Format(time.RFC3339Nano) + " " + text + "\n"
	}
	zerolog := func(at time.Time, source, text string) string {
		return line(at, "\x1b[90m"+at.UTC().Format("2006/01/02 03:04PM")+"\x1b[0m \x1b[32mINF\x1b[0m \x1b[1mgithub.com/portainer/portainer/api/"+
			source+"\x1b[0m\x1b[36m >\x1b[0m "+text)
	}
	var b strings.Builder
	b.WriteString(zerolog(started, "http/security/setuptoken/setuptoken.go:40", ""))
	for _, text := range []string{"", "==========================", "", "setup_token=" + hex.EncodeToString(sum[:]), "",
		"Paste it into the setup screen, or send it in the X-Setup-Token header.", "Start with --no-setup-token to disable.", "",
		"========================== |"} {
		b.WriteString(line(started, text))
	}
	b.WriteString(zerolog(started, "http/server.go:371", "starting HTTPS server | \x1b[36mbind_address=\x1b[0m:9443"))
	if lock := started.Add(5 * time.Minute); !now.Before(lock) {
		b.WriteString(zerolog(lock, "adminmonitor/admin_monitor.go:60", "the Portainer instance timed out for security purposes, "+
			"to re-enable your Portainer instance, you will need to restart Portainer |"))
	}
	return b.String()
}
