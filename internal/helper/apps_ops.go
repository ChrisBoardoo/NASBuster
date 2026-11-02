// SPDX-License-Identifier: AGPL-3.0-or-later

package helper

import (
	"context"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/crosswax/nasbuster/internal/apps"
	"github.com/crosswax/nasbuster/internal/storage"
)

// Applications (ADR-0013). Long work (apt, image pulls) runs in transient
// systemd units: it survives a daemon restart and logs to the journal.
// Compose files are passed to `docker compose` as files, never interpreted
// here and never through a shell.

// engineStatus reports Docker's installation, service and configuration.
func (o *Ops) engineStatus(ctx context.Context) EngineStatus {
	st := EngineStatus{Job: o.job(ctx, apps.EngineJob, 30)}
	res, err := o.Run.Run(ctx, "dpkg-query", "-W", "-f", "${Package} ${db:Status-Status} ${Version}\n", apps.Packages[0], apps.Packages[1], apps.Packages[2])
	if err == nil {
		installed := 0
		for _, line := range strings.Split(string(res.Stdout), "\n") {
			f := strings.Fields(line)
			if len(f) == 3 && f[1] == "installed" {
				installed++
				if f[0] == "docker.io" {
					st.Version = f[2]
				}
			}
		}
		st.Installed = installed == len(apps.Packages)
	}
	if res, err := o.Run.Run(ctx, "systemctl", "is-active", "docker.service"); err == nil {
		st.Active = strings.TrimSpace(string(res.Stdout)) == "active"
	}
	if b, err := o.Files.ReadFile(apps.DaemonJSON); err == nil {
		st.Pool = apps.EnginePool(b)
	}
	st.DataPools = []EngineData{}
	if !st.Installed {
		st.DataPools = o.engineDataPools()
	}
	return st
}

// engineDataPools finds the Docker data folders an earlier installation left
// on the mounted pools (a new SD card, or Docker uninstalled with its data
// kept). Installing Docker on one of them takes its containers back (P9).
func (o *Ops) engineDataPools() []EngineData {
	out := []EngineData{}
	pools, err := o.Files.ReadDir(storage.MountRoot)
	if err != nil {
		return out
	}
	for _, pool := range pools {
		if !storage.ValidPoolName(pool) || !o.Files.Exists(apps.EngineDir(pool)) {
			continue
		}
		containers, _ := o.Files.ReadDir(apps.EngineDir(pool) + "/containers")
		out = append(out, EngineData{Pool: pool, Containers: len(containers)})
	}
	return out
}

// engineConfigRefresh turns live-restore off in the daemon.json NasBuster wrote
// (ADR-0029), then has Docker reload it: running containers keep running.
// A daemon.json without our data-root on a pool is never touched.
func (o *Ops) engineConfigRefresh(ctx context.Context) (EngineRefreshResult, *Error) {
	var out EngineRefreshResult
	b, err := o.Files.ReadFile(apps.DaemonJSON)
	if err != nil || apps.EnginePool(b) == "" {
		return out, nil
	}
	nb, ok := apps.WithoutLiveRestore(b)
	if !ok {
		return out, nil
	}
	if err := o.Files.WriteFile(apps.DaemonJSON, nb, 0o644); err != nil {
		return out, errorf(CodeFailed, "write %s: %v", apps.DaemonJSON, err)
	}
	out.Updated = true
	if res, err := o.Run.Run(ctx, "systemctl", "is-active", "docker.service"); err == nil && strings.TrimSpace(string(res.Stdout)) == "active" {
		if err := o.check(ctx, "systemctl", "reload", "docker.service"); err != nil {
			return out, err
		}
		out.Reloaded = true
	}
	return out, nil
}

// job reads a transient unit's state and the end of its journal.
func (o *Ops) job(ctx context.Context, job string, logLines int) apps.Job {
	unit := apps.JobUnit(job)
	res, err := o.Run.Run(ctx, "systemctl", "show", unit, "-p", "LoadState,ActiveState,Result,ExecMainStatus")
	if err != nil {
		return apps.Job{State: "none", Log: []string{}}
	}
	j := apps.ParseJob(string(res.Stdout))
	if j.State != "none" && logLines > 0 {
		if lr, err := o.Run.Run(ctx, "journalctl", "-u", unit, "-n", strconv.Itoa(logLines), "--no-pager", "-o", "cat"); err == nil {
			for _, line := range strings.Split(strings.TrimRight(string(lr.Stdout), "\n"), "\n") {
				if len(line) > 300 {
					line = line[:300] + "…"
				}
				if line != "" {
					j.Log = append(j.Log, line)
				}
			}
		}
	}
	return j
}

// jobState is the state of a job's unit, or an error when systemctl could not
// say: unlike job, which reads a failed probe as "none".
func (o *Ops) jobState(ctx context.Context, job string) (string, *Error) {
	res, err := o.Run.Run(ctx, "systemctl", "show", apps.JobUnit(job), "-p", "LoadState,ActiveState,Result,ExecMainStatus")
	if err != nil {
		return "", errorf(CodeFailed, "systemctl show: %v", err)
	}
	if res.ExitCode != 0 {
		return "", errorf(CodeFailed, "systemctl show exited with %d: %s", res.ExitCode, firstLine(res.Stderr))
	}
	return apps.ParseJob(string(res.Stdout)).State, nil
}

// forgetUnit stops and forgets the previous run of a transient unit. A first
// run has none ("not loaded"): only other failures are logged.
func (o *Ops) forgetUnit(ctx context.Context, unit string) {
	for _, verb := range []string{"stop", "reset-failed"} {
		err := o.check(ctx, "systemctl", verb, unit)
		if err != nil && !strings.Contains(err.Message, "not loaded") {
			o.logger().Warn("could not clear the previous unit", "unit", unit, "cmd", verb, "err", err.Message)
		}
	}
}

// startJob runs cmd in a transient unit. pre, post and cmd are fixed by the
// code (validated names and computed paths only).
func (o *Ops) startJob(ctx context.Context, job, workDir string, env, pre, post, cmd []string) *Error {
	return o.startUnit(ctx, job, "CrossWax NasBuster "+job, workDir, env, pre, post, cmd)
}

// startJobDesc runs cmd with a description that carries data for later reads.
func (o *Ops) startJobDesc(ctx context.Context, job, desc string, cmd []string) *Error {
	return o.startUnit(ctx, job, desc, "", nil, nil, nil, cmd)
}

// startUnit starts a transient oneshot unit. Its command line is passed as
// is: no ${VAR} expansion (on by default in systemd-run since 254; a user's
// folder name must stay what it is), and systemd does not expand specifiers
// there either (verified with systemd 257). pre and post ARE parsed by
// systemd: fixed strings only.
func (o *Ops) startUnit(ctx context.Context, job, desc, workDir string, env, pre, post, cmd []string) *Error {
	if o.job(ctx, job, 0).State == "running" {
		return errorf(CodeJobRunning, "%s is already running", job)
	}
	unit := apps.JobUnit(job)
	o.forgetUnit(ctx, unit)
	args := []string{"--unit=" + strings.TrimSuffix(unit, ".service"), "--no-block", "--expand-environment=no",
		"--description=" + desc,
		"-p", "Type=oneshot", "-p", "RemainAfterExit=yes", "-p", "TimeoutStartSec=" + jobTimeout(job)}
	if workDir != "" {
		args = append(args, "-p", "WorkingDirectory="+workDir)
	}
	for _, e := range env {
		args = append(args, "--setenv="+e)
	}
	for _, p := range pre {
		args = append(args, "-p", "ExecStartPre="+p)
	}
	for _, p := range post {
		args = append(args, "-p", "ExecStartPost="+p)
	}
	return o.check(ctx, "systemd-run", append(args, cmd...)...)
}

// engineInstall configures Docker to keep its data on a pool, THEN installs
// it: the package starts Docker right away, and it must never start on the
// SD card.
func (o *Ops) engineInstall(ctx context.Context, a EngineInstallArgs) (map[string]bool, *Error) {
	if err := o.requireMountedPool(ctx, a.Pool); err != nil {
		return nil, err
	}
	st := o.engineStatus(ctx)
	if st.Installed {
		return nil, errorf(CodeEngineInstalled, "Docker is already installed")
	}
	if st.Job.State == "running" {
		return nil, errorf(CodeJobRunning, "the installation is already running")
	}
	dir := apps.EngineDir(a.Pool)
	if !o.Files.Exists(dir) {
		if err := o.check(ctx, "btrfs", "subvolume", "create", dir); err != nil {
			return nil, err
		}
	}
	if err := o.Files.MkdirAll(path.Dir(apps.DaemonJSON), 0o755); err != nil {
		return nil, errorf(CodeFailed, "create /etc/docker: %v", err)
	}
	if err := o.Files.WriteFile(apps.DaemonJSON, apps.DaemonConfig(a.Pool), 0o644); err != nil {
		return nil, errorf(CodeFailed, "write daemon.json: %v", err)
	}
	dropIn := path.Join(o.UnitDir, apps.DropInName)
	if err := o.Files.MkdirAll(path.Dir(dropIn), 0o755); err != nil {
		return nil, errorf(CodeFailed, "create drop-in directory: %v", err)
	}
	if err := o.Files.WriteFile(dropIn, []byte(apps.DropIn(a.Pool)), 0o644); err != nil {
		return nil, errorf(CodeFailed, "write drop-in: %v", err)
	}
	if err := o.check(ctx, "systemctl", "daemon-reload"); err != nil {
		return nil, err
	}
	install := append([]string{"/usr/bin/apt-get", "install", "-y", "-qq", "--no-install-recommends", "-o", "DPkg::Lock::Timeout=600"}, apps.Packages...)
	// Start Docker explicitly: a package's postinst may be forbidden to start
	// services (policy-rc.d in images built from containers).
	if err := o.startJob(ctx, apps.EngineJob, "", []string{"DEBIAN_FRONTEND=noninteractive"},
		[]string{"/usr/bin/apt-get update -qq -o DPkg::Lock::Timeout=600"},
		[]string{"/usr/bin/systemctl enable --now docker.socket docker.service"}, install); err != nil {
		return nil, err
	}
	return map[string]bool{"started": true}, nil
}

func (o *Ops) requireEngine(ctx context.Context) *Error {
	if st := o.engineStatus(ctx); !st.Installed || !st.Active {
		return errorf(CodeEngineMissing, "Docker is not installed or not running")
	}
	return nil
}

func validApp(pool, name string) *Error {
	if !apps.ValidName(name) {
		return errorf(CodeAppName, "invalid application name")
	}
	if !storage.ValidPoolName(pool) {
		return errorf(CodePoolName, "invalid pool name")
	}
	return nil
}

// appDeploy writes, validates and (re)starts an application.
func (o *Ops) appDeploy(ctx context.Context, a AppDeployArgs) (map[string]bool, *Error) {
	if err := validApp(a.Pool, a.Name); err != nil {
		return nil, err
	}
	if len(a.Compose) == 0 || len(a.Compose) > apps.MaxCompose {
		return nil, errorf(CodeComposeInvalid, "the compose file must be 1 byte to 64 KiB")
	}
	if err := o.requireMountedPool(ctx, a.Pool); err != nil {
		return nil, err
	}
	if err := o.requireEngine(ctx); err != nil {
		return nil, err
	}
	job := "app-" + a.Name
	if o.job(ctx, job, 0).State == "running" {
		return nil, errorf(CodeJobRunning, "%s is already being deployed", a.Name)
	}
	if !o.Files.Exists(apps.AppsDir(a.Pool)) {
		if err := o.Files.MkdirAll(apps.AppsDir(a.Pool), 0o755); err != nil {
			return nil, errorf(CodeFailed, "create %s: %v", apps.AppsDir(a.Pool), err)
		}
	}
	dir := apps.AppDir(a.Pool, a.Name)
	created := false
	if !o.Files.Exists(dir) {
		if err := o.check(ctx, "btrfs", "subvolume", "create", dir); err != nil {
			return nil, err
		}
		created = true
	}
	// A refused new application leaves nothing behind (the pool stays destroyable).
	undo := func() {
		if created {
			o.Files.Remove(path.Join(dir, ".compose.new.yaml"))
			o.try(ctx, "btrfs", "subvolume", "delete", dir)
			o.Files.Remove(apps.AppsDir(a.Pool)) // only if now empty
		}
	}
	// Validate a candidate next to the real file (relative paths resolve the same).
	candidate := path.Join(dir, ".compose.new.yaml")
	if err := o.Files.WriteFile(candidate, []byte(a.Compose), 0o600); err != nil { // may hold secrets
		return nil, errorf(CodeFailed, "write compose file: %v", err)
	}
	defer o.Files.Remove(candidate)
	vctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	res, err := o.Run.Run(vctx, "docker", "compose", "-p", apps.Project(a.Name), "-f", candidate, "config", "--format", "json")
	if err != nil {
		undo()
		return nil, errorf(CodeFailed, "docker compose: %v", err)
	}
	if res.ExitCode != 0 {
		undo()
		return nil, errorf(CodeComposeInvalid, "%s", firstLine(res.Stderr)).
			with(map[string]any{"detail": firstLine(res.Stderr)})
	}
	sources, perr := apps.BindSources(res.Stdout)
	if perr != nil {
		undo()
		return nil, errorf(CodeFailed, "%v", perr)
	}
	// Folders first: a refusal leaves the deployed compose file as it was.
	if err := o.createBindSources(a.Pool, a.Name, sources); err != nil {
		undo()
		return nil, err
	}
	compose := apps.ComposePath(a.Pool, a.Name)
	if err := o.Files.WriteFile(compose, []byte(a.Compose), 0o600); err != nil {
		undo()
		return nil, errorf(CodeFailed, "write compose file: %v", err)
	}
	if err := o.startJob(ctx, job, dir, nil, nil, nil, []string{"/usr/bin/docker", "compose", "-p", apps.Project(a.Name),
		"-f", compose, "up", "-d", "--remove-orphans", "--quiet-pull"}); err != nil {
		undo()
		return nil, err
	}
	return map[string]bool{"started": true}, nil
}

// createBindSources creates the missing bind sources of an application that
// lie inside its own folder, as directories (ADR-0036). Only a deployment,
// asked for by the administrator, does it: the catalog forbids Docker to
// (create_host_path: false), so that a container whose data is gone refuses
// to start instead of coming back empty. Paths elsewhere are left to Docker.
func (o *Ops) createBindSources(pool, name string, sources []string) *Error {
	for _, src := range sources {
		rel, ok := apps.InAppDir(pool, name, src)
		if !ok || o.Files.Exists(src) {
			continue
		}
		if err := o.Files.MkdirAllIn(apps.AppDir(pool, name), rel, 0o755); err != nil {
			return errorf(CodeFailed, "create %s: %v", src, err)
		}
	}
	return nil
}

// appOrphan stops or removes the containers of a CWNSB project whose
// application is not recorded (ADR-0036): Docker restarted them by itself.
// By project name only: the application's files are neither read nor touched.
func (o *Ops) appOrphan(ctx context.Context, a OrphanArgs) (map[string]bool, *Error) {
	if !apps.ValidName(a.Name) {
		return nil, errorf(CodeAppName, "invalid application name")
	}
	var verb []string
	switch a.Action {
	case "stop":
		verb = []string{"stop"}
	case "remove":
		verb = []string{"down", "--remove-orphans"}
	default:
		return nil, errorf(CodeInvalidArgs, "unknown action")
	}
	if err := o.requireEngine(ctx); err != nil {
		return nil, err
	}
	if o.job(ctx, "app-"+a.Name, 0).State == "running" {
		return nil, errorf(CodeJobRunning, "%s is being deployed", a.Name)
	}
	if err := o.checkTimeout(ctx, 5*time.Minute, "docker", append([]string{"compose", "-p", apps.Project(a.Name)}, verb...)...); err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, nil
}

// appAction starts, stops or restarts an application's containers.
func (o *Ops) appAction(ctx context.Context, a AppActionArgs) (map[string]bool, *Error) {
	if err := validApp(a.Pool, a.Name); err != nil {
		return nil, err
	}
	switch a.Action {
	case "start", "stop", "restart":
	default:
		return nil, errorf(CodeInvalidArgs, "unknown action")
	}
	if err := o.requireEngine(ctx); err != nil {
		return nil, err
	}
	compose := apps.ComposePath(a.Pool, a.Name)
	if !o.Files.Exists(compose) {
		return nil, errorf(CodeAppNotFound, "no application %s on pool %s", a.Name, a.Pool)
	}
	if err := o.checkTimeout(ctx, 3*time.Minute, "docker", "compose", "-p", apps.Project(a.Name), "-f", compose, a.Action); err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, nil
}

// appRemove stops and removes an application's containers and networks; its
// data (compose file and ./ folders) is deleted only when asked, and then
// kept for TrashDays in a read-only snapshot first (ADR-0035).
func (o *Ops) appRemove(ctx context.Context, a AppRemoveArgs) (map[string]bool, *Error) {
	if err := validApp(a.Pool, a.Name); err != nil {
		return nil, err
	}
	if err := o.requireMountedPool(ctx, a.Pool); err != nil {
		return nil, err
	}
	compose := apps.ComposePath(a.Pool, a.Name)
	if o.requireEngine(ctx) == nil {
		args := []string{"compose", "-p", apps.Project(a.Name)}
		if o.Files.Exists(compose) {
			args = append(args, "-f", compose)
		}
		if err := o.checkTimeout(ctx, 5*time.Minute, "docker", append(args, "down", "--remove-orphans")...); err != nil {
			return nil, err
		}
	}
	unit := apps.JobUnit("app-" + a.Name)
	o.try(ctx, "systemctl", "stop", unit)
	o.try(ctx, "systemctl", "reset-failed", unit)
	dir := apps.AppDir(a.Pool, a.Name)
	if a.DeleteData && o.Files.Exists(dir) {
		if err := o.check(ctx, "btrfs", "subvolume", "show", dir); err != nil {
			return nil, errorf(CodeFailed, "%s is not a subvolume: refusing to delete it", dir)
		}
		// A wrong tick is not final: no snapshot, no deletion.
		trash := apps.TrashDir(a.Pool)
		if err := o.Files.MkdirAll(trash, 0o700); err != nil {
			return nil, errorf(CodeFailed, "create %s: %v", trash, err)
		}
		kept := path.Join(trash, apps.TrashEntry(a.Name, o.now()))
		if err := o.checkTimeout(ctx, time.Minute, "btrfs", "subvolume", "snapshot", "-r", dir, kept); err != nil {
			return nil, err
		}
		if err := o.checkTimeout(ctx, 5*time.Minute, "btrfs", "subvolume", "delete", dir); err != nil {
			return nil, err
		}
		o.Files.Remove(apps.AppsDir(a.Pool)) // only if now empty: the pool stays destroyable
	}
	return map[string]bool{"removed": true}, nil
}

// appTrashPurge deletes the kept data of applications removed more than
// TrashDays ago (ADR-0035). Only entries named by TrashEntry are touched.
func (o *Ops) appTrashPurge(ctx context.Context, a TrashPurgeArgs) (map[string]int, *Error) {
	if !storage.ValidPoolName(a.Pool) {
		return nil, errorf(CodePoolName, "invalid pool name")
	}
	trash := apps.TrashDir(a.Pool)
	entries, err := o.Files.ReadDir(trash)
	if err != nil {
		return map[string]int{"purged": 0}, nil // nothing kept, or the pool is not mounted
	}
	if err := o.requireMountedPool(ctx, a.Pool); err != nil {
		return nil, err
	}
	limit := o.now().Add(-apps.TrashDays * 24 * time.Hour)
	purged := 0
	for _, e := range entries {
		if _, at, ok := apps.ParseTrashEntry(e); !ok || at.After(limit) {
			continue
		}
		if err := o.checkTimeout(ctx, 5*time.Minute, "btrfs", "subvolume", "delete", path.Join(trash, e)); err != nil {
			return nil, err
		}
		purged++
	}
	return map[string]int{"purged": purged}, nil
}

// appLogs returns the end of an application's container logs.
func (o *Ops) appLogs(ctx context.Context, a AppLogsArgs) (map[string]string, *Error) {
	if err := validApp(a.Pool, a.Name); err != nil {
		return nil, err
	}
	if a.Tail < 1 || a.Tail > 2000 {
		return nil, errorf(CodeInvalidArgs, "tail must be between 1 and 2000")
	}
	if err := o.requireEngine(ctx); err != nil {
		return nil, err
	}
	compose := apps.ComposePath(a.Pool, a.Name)
	if !o.Files.Exists(compose) {
		return nil, errorf(CodeAppNotFound, "no application %s on pool %s", a.Name, a.Pool)
	}
	lctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	res, err := o.Run.Run(lctx, "docker", "compose", "-p", apps.Project(a.Name), "-f", compose, "logs", "--no-color", "--timestamps", "--tail", strconv.Itoa(a.Tail))
	if err != nil {
		return nil, errorf(CodeFailed, "docker compose logs: %v", err)
	}
	out := string(res.Stdout) + string(res.Stderr)
	if len(out) > 512<<10 {
		out = out[len(out)-512<<10:]
	}
	return map[string]string{"logs": out}, nil
}

// appsStatus lists every Compose project (CWNSB's and others) with its containers.
func (o *Ops) appsStatus(ctx context.Context) (AppsStatus, *Error) {
	out := AppsStatus{Engine: o.engineStatus(ctx), Projects: []ProjectView{}}
	if !out.Engine.Active {
		return out, nil
	}
	res, err := o.Run.Run(ctx, "docker", "compose", "ls", "--all", "--format", "json")
	if err != nil || res.ExitCode != 0 {
		return out, errorf(CodeFailed, "docker compose ls: %s", firstLine(res.Stderr))
	}
	projects, perr := apps.ParseProjects(res.Stdout)
	if perr != nil {
		return out, errorf(CodeFailed, "%v", perr)
	}
	for _, p := range projects {
		v := ProjectView{ProjectStatus: p, Containers: []apps.Container{}}
		if name, ok := strings.CutPrefix(p.Name, "cwnsb-"); ok && apps.ValidName(name) {
			v.App = name
		}
		if r, err := o.Run.Run(ctx, "docker", "compose", "-p", p.Name, "ps", "--all", "--format", "json"); err == nil && r.ExitCode == 0 {
			if cs, err := apps.ParseContainers(r.Stdout); err == nil {
				v.Containers = cs
			}
		}
		out.Projects = append(out.Projects, v)
	}
	return out, nil
}

// appJob reports a job (engine installation or application deployment).
func (o *Ops) appJob(ctx context.Context, a JobArgs) (apps.Job, *Error) {
	if !apps.ValidJob(a.Job) {
		return apps.Job{}, errorf(CodeInvalidArgs, "invalid job")
	}
	return o.job(ctx, a.Job, 40), nil
}

// appUpdate pulls newer images and recreates what changed, in the
// background (the same job as a deployment).
func (o *Ops) appUpdate(ctx context.Context, a AppActionArgs) (map[string]bool, *Error) {
	if err := validApp(a.Pool, a.Name); err != nil {
		return nil, err
	}
	if err := o.requireEngine(ctx); err != nil {
		return nil, err
	}
	compose := apps.ComposePath(a.Pool, a.Name)
	if !o.Files.Exists(compose) {
		return nil, errorf(CodeAppNotFound, "no application %s on pool %s", a.Name, a.Pool)
	}
	project := apps.Project(a.Name)
	pull := "/usr/bin/docker compose -p " + project + " -f " + compose + " pull --quiet"
	if err := o.startJob(ctx, "app-"+a.Name, apps.AppDir(a.Pool, a.Name), nil, []string{pull}, nil,
		[]string{"/usr/bin/docker", "compose", "-p", project, "-f", compose, "up", "-d", "--remove-orphans"}); err != nil {
		return nil, err
	}
	return map[string]bool{"started": true}, nil
}

// engineRemove uninstalls Docker. Refused while any container exists (an
// application, or anything created by hand or by Portainer).
func (o *Ops) engineRemove(ctx context.Context, a EngineRemoveArgs) (map[string]bool, *Error) {
	st := o.engineStatus(ctx)
	if !st.Installed {
		// Uninstalled earlier with its data kept: delete that data now.
		if a.DeleteData && storage.ValidPoolName(a.Pool) && o.Files.Exists(apps.EngineDir(a.Pool)) {
			if err := o.requireMountedPool(ctx, a.Pool); err != nil {
				return nil, err
			}
			if err := o.checkTimeout(ctx, 10*time.Minute, "btrfs", "subvolume", "delete", apps.EngineDir(a.Pool)); err != nil {
				return nil, err
			}
			return map[string]bool{"removed": true}, nil
		}
		return nil, errorf(CodeEngineMissing, "Docker is not installed")
	}
	if st.Job.State == "running" {
		return nil, errorf(CodeJobRunning, "a Docker installation is running")
	}
	if st.Active {
		res, err := o.Run.Run(ctx, "docker", "ps", "--all", "--quiet")
		if err != nil || res.ExitCode != 0 {
			return nil, errorf(CodeFailed, "list containers: %s", firstLine(res.Stderr))
		}
		if n := len(strings.Fields(string(res.Stdout))); n > 0 {
			return nil, errorf(CodeEngineInUse, "%d containers still exist: remove the applications first", n).
				with(map[string]any{"containers": n})
		}
	}
	o.try(ctx, "systemctl", "stop", "docker.socket", "docker.service", "containerd.service")
	purge := append([]string{"purge", "-y", "-qq", "-o", "DPkg::Lock::Timeout=600"}, apps.Packages...)
	purge = append(purge, "containerd", "runc")
	if err := o.checkTimeout(ctx, 10*time.Minute, "apt-get", purge...); err != nil {
		return nil, err
	}
	_ = o.Files.Remove(apps.DaemonJSON)
	_ = o.Files.Remove(path.Dir(apps.DaemonJSON)) // only if now empty
	dropIn := path.Join(o.UnitDir, apps.DropInName)
	_ = o.Files.Remove(dropIn)
	_ = o.Files.Remove(path.Dir(dropIn))
	o.try(ctx, "systemctl", "daemon-reload")
	unit := apps.JobUnit(apps.EngineJob)
	o.try(ctx, "systemctl", "stop", unit)
	o.try(ctx, "systemctl", "reset-failed", unit)
	if a.DeleteData && st.Pool != "" {
		dir := apps.EngineDir(st.Pool)
		if o.Files.Exists(dir) {
			if err := o.checkTimeout(ctx, 10*time.Minute, "btrfs", "subvolume", "delete", dir); err != nil {
				return nil, err
			}
		}
	}
	return map[string]bool{"removed": true}, nil
}

// jobTimeout: a copy, a scrub or a disk replacement may take hours; a stuck
// download must not block the next deployment forever.
func jobTimeout(job string) string {
	kind, _, _ := strings.Cut(job, "-")
	switch kind {
	case "import", apps.ScrubJob, apps.BalanceJob, apps.ReplaceJob, apps.ResyncJob:
		return "infinity"
	}
	return "45min"
}
