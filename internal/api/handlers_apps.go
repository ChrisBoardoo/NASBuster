// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/crosswax/nasbuster/internal/apps"
	"github.com/crosswax/nasbuster/internal/helper"
	"github.com/crosswax/nasbuster/internal/store"
)

// Applications (ADR-0013). The database holds the desired state (the compose
// file); Docker is asked for the live state on every read.

// appView is an application with its live state.
type appView struct {
	store.App
	Status     string           `json:"status"` // compose status ("running(1)"), "" if not deployed
	Containers []apps.Container `json:"containers"`
	Job        apps.Job         `json:"job"`
}

func (s *Server) appsCtx(r *http.Request, user store.User, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(helper.WithActor(context.WithoutCancel(r.Context()), user.Username), d)
}

func (s *Server) handleApps(w http.ResponseWriter, r *http.Request, _ store.User) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	recorded, err := s.d.Store.Apps(ctx)
	if err != nil {
		s.internalError(w, "list apps", err)
		return
	}
	var st helper.AppsStatus
	if err := s.d.Helper.Call(ctx, helper.OpAppsStatus, nil, &st); err != nil {
		s.respondHelperError(w, "apps.status", err) // polled: never audited
		return
	}
	isRecorded := map[string]bool{}
	for _, a := range recorded {
		isRecorded[a.Name] = true
	}
	live := map[string]helper.ProjectView{}
	others := []helper.ProjectView{}
	orphans := []orphanView{}
	for _, p := range st.Projects {
		switch {
		case p.App == "":
			others = append(others, p)
		case isRecorded[p.App]:
			live[p.App] = p
		default:
			orphans = append(orphans, orphanView{ProjectView: p, Pool: apps.ProjectPool(p.App, p.ConfigFiles)})
		}
	}
	views := make([]appView, 0, len(recorded))
	for _, a := range recorded {
		v := appView{App: a, Containers: []apps.Container{}, Job: apps.Job{State: "none", Log: []string{}}}
		if p, ok := live[a.Name]; ok {
			v.Status, v.Containers = p.Status, p.Containers
		}
		var job apps.Job
		if err := s.d.Helper.Call(ctx, helper.OpAppJob, helper.JobArgs{Job: "app-" + a.Name}, &job); err == nil {
			v.Job = job
		}
		views = append(views, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"engine": st.Engine, "apps": views, "others": others, "orphans": orphans})
}

// orphanView is a CWNSB project whose application is not recorded: removed
// while its containers were in another Docker, or a restored configuration
// older than it. Docker restarts its containers by itself (ADR-0036).
type orphanView struct {
	helper.ProjectView
	Pool string `json:"pool"` // where its compose file is, "" if not where NasBuster puts it
}

// handleOrphanAction stops or removes the containers of an orphan project;
// its files stay as they are.
func (s *Server) handleOrphanAction(w http.ResponseWriter, r *http.Request, user store.User) {
	name, action := r.PathValue("name"), r.PathValue("action")
	if !apps.ValidName(name) || (action != "stop" && action != "remove") {
		writeError(w, http.StatusNotFound, "request.not_found", "Unknown orphan or action", nil)
		return
	}
	if _, err := s.d.Store.AppByName(r.Context(), name); err == nil {
		writeError(w, http.StatusConflict, "apps.not_orphan", "This application is recorded: use its own actions", nil)
		return
	} else if !errors.Is(err, store.ErrNotFound) {
		s.internalError(w, "load app", err)
		return
	}
	ctx, cancel := s.appsCtx(r, user, 6*time.Minute)
	defer cancel()
	if err := s.d.Helper.Call(ctx, helper.OpAppOrphan, helper.OrphanArgs{Name: name, Action: action}, nil); err != nil {
		s.helperError(w, r, user, "app.orphan_"+action, err)
		return
	}
	s.audit(r, user.Username, "app.orphan_"+action, name, "ok", "")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleEngine(w http.ResponseWriter, r *http.Request, _ store.User) {
	var st helper.EngineStatus
	if err := s.d.Helper.Call(r.Context(), helper.OpEngineStatus, nil, &st); err != nil {
		s.respondHelperError(w, "apps.engine_status", err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleEngineInstall(w http.ResponseWriter, r *http.Request, user store.User) {
	var body struct {
		Pool string `json:"pool"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if _, err := s.d.Store.PoolByName(r.Context(), body.Pool); err != nil {
		writeError(w, http.StatusNotFound, "storage.pool_not_found", "Pool not found", nil)
		return
	}
	ctx, cancel := s.appsCtx(r, user, time.Minute)
	defer cancel()
	if err := s.d.Helper.Call(ctx, helper.OpEngineInstall, helper.EngineInstallArgs{Pool: body.Pool}, nil); err != nil {
		s.helperError(w, r, user, "apps.engine_install", err)
		return
	}
	s.audit(r, user.Username, "apps.engine_install", body.Pool, "ok", "")
	writeJSON(w, http.StatusAccepted, map[string]bool{"started": true})
}

func (s *Server) handleCatalog(w http.ResponseWriter, _ *http.Request, _ store.User) {
	writeJSON(w, http.StatusOK, map[string]any{"templates": apps.Catalog()})
}

func (s *Server) handleCreateApp(w http.ResponseWriter, r *http.Request, user store.User) {
	var body struct {
		Name     string `json:"name"`
		Pool     string `json:"pool"`
		Template string `json:"template"`
		Compose  string `json:"compose"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if !apps.ValidName(body.Name) {
		writeError(w, http.StatusBadRequest, "validation.app_name", "Invalid application name", nil)
		return
	}
	if _, err := s.d.Store.AppByName(r.Context(), body.Name); err == nil {
		writeError(w, http.StatusConflict, "apps.exists", "An application with this name exists", nil)
		return
	}
	if _, err := s.d.Store.PoolByName(r.Context(), body.Pool); err != nil {
		writeError(w, http.StatusNotFound, "storage.pool_not_found", "Pool not found", nil)
		return
	}
	if body.Template != "" {
		t, ok := apps.TemplateByName(body.Template)
		if !ok {
			writeError(w, http.StatusNotFound, "apps.template_not_found", "Unknown template", nil)
			return
		}
		if body.Compose == "" {
			body.Compose = t.Compose
		}
	}
	s.deployApp(w, r, user, store.App{Name: body.Name, Pool: body.Pool, Template: body.Template, Compose: body.Compose}, "app.create")
}

func (s *Server) handleUpdateApp(w http.ResponseWriter, r *http.Request, user store.User) {
	a, ok := s.lookupApp(w, r)
	if !ok {
		return
	}
	var body struct {
		Compose string `json:"compose"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	a.Compose = body.Compose
	s.deployApp(w, r, user, a, "app.update")
}

// deployApp asks the helper to validate and start the compose file, and
// records it only once the helper accepted it.
func (s *Server) deployApp(w http.ResponseWriter, r *http.Request, user store.User, a store.App, action string) {
	ctx, cancel := s.appsCtx(r, user, 2*time.Minute)
	defer cancel()
	args := helper.AppDeployArgs{Pool: a.Pool, Name: a.Name, Compose: a.Compose}
	if err := s.d.Helper.Call(ctx, helper.OpAppDeploy, args, nil); err != nil {
		s.helperError(w, r, user, action, err)
		return
	}
	saved, err := s.d.Store.SaveApp(ctx, a)
	if err != nil {
		s.internalError(w, "record app", err)
		return
	}
	s.audit(r, user.Username, action, a.Name, "ok", a.Pool)
	writeJSON(w, http.StatusAccepted, map[string]any{"app": saved})
}

func (s *Server) lookupApp(w http.ResponseWriter, r *http.Request) (store.App, bool) {
	name := r.PathValue("name")
	if !apps.ValidName(name) {
		writeError(w, http.StatusNotFound, "apps.not_found", "Application not found", nil)
		return store.App{}, false
	}
	a, err := s.d.Store.AppByName(r.Context(), name)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "apps.not_found", "Application not found", nil)
		return store.App{}, false
	}
	if err != nil {
		s.internalError(w, "load app", err)
		return store.App{}, false
	}
	return a, true
}

func (s *Server) handleAppAction(w http.ResponseWriter, r *http.Request, user store.User) {
	a, ok := s.lookupApp(w, r)
	if !ok {
		return
	}
	action := r.PathValue("action")
	op, status := helper.OpAppAction, http.StatusOK
	switch action {
	case "start", "stop", "restart":
	case "update": // pull newer images, recreate: in the background
		op, status = helper.OpAppUpdate, http.StatusAccepted
	default:
		writeError(w, http.StatusNotFound, "request.not_found", "Unknown action", nil)
		return
	}
	ctx, cancel := s.appsCtx(r, user, 4*time.Minute)
	defer cancel()
	if err := s.d.Helper.Call(ctx, op, helper.AppActionArgs{Pool: a.Pool, Name: a.Name, Action: action}, nil); err != nil {
		s.helperError(w, r, user, "app."+action, err)
		return
	}
	s.audit(r, user.Username, "app."+action, a.Name, "ok", "")
	writeJSON(w, status, map[string]bool{"ok": true})
}

func (s *Server) handleAppLogs(w http.ResponseWriter, r *http.Request, _ store.User) {
	a, ok := s.lookupApp(w, r)
	if !ok {
		return
	}
	tail := 200
	if v := r.URL.Query().Get("tail"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 2000 {
			writeError(w, http.StatusBadRequest, "validation.tail", "tail must be between 1 and 2000", nil)
			return
		}
		tail = n
	}
	var out map[string]string
	if err := s.d.Helper.Call(r.Context(), helper.OpAppLogs, helper.AppLogsArgs{Pool: a.Pool, Name: a.Name, Tail: tail}, &out); err != nil {
		s.respondHelperError(w, "app.logs", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// setupTokenView tells the UI whether an application (Portainer) waits for
// its admin account. The token is sent only while it can still be used; the
// account itself is created by the administrator in the application.
type setupTokenView struct {
	State     string     `json:"state"` // none, waiting, locked, created
	Token     string     `json:"token,omitempty"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"` // when it locks itself; absent if unknown
}

// setupTokenLogTail covers a few restarts of Portainer (about 20 lines each).
const setupTokenLogTail = 300

func (s *Server) handleAppSetupToken(w http.ResponseWriter, r *http.Request, _ store.User) {
	a, ok := s.lookupApp(w, r)
	if !ok {
		return
	}
	t, ok := apps.TemplateByName(a.Template)
	if !ok || !t.SetupToken {
		writeError(w, http.StatusNotFound, "apps.no_setup_token", "This application has no setup token", nil)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var st helper.AppsStatus
	if err := s.d.Helper.Call(ctx, helper.OpAppsStatus, nil, &st); err != nil {
		s.respondHelperError(w, "apps.status", err)
		return
	}
	port := 0
	for _, p := range st.Projects {
		if p.App != a.Name {
			continue
		}
		for _, c := range p.Containers {
			for _, cp := range c.Ports {
				if c.State == "running" && cp.TargetPort == t.WebPort && cp.Protocol == "tcp" {
					port = cp.PublishedPort
				}
			}
		}
	}
	if port == 0 { // stopped, or its web port is no longer published
		writeJSON(w, http.StatusOK, setupTokenView{State: "none"})
		return
	}
	var out map[string]string
	if err := s.d.Helper.Call(ctx, helper.OpAppLogs, helper.AppLogsArgs{Pool: a.Pool, Name: a.Name, Tail: setupTokenLogTail}, &out); err != nil {
		s.respondHelperError(w, "app.logs", err)
		return
	}
	tok, found := apps.ParseSetupToken(out["logs"])
	if !found {
		writeJSON(w, http.StatusOK, setupTokenView{State: "none"})
		return
	}
	check := s.d.AdminCheck
	if check == nil {
		check = apps.AdminExists
	}
	created, err := check(ctx, t.WebScheme, port)
	if err != nil {
		// Unreachable (still starting, or locked): the logs decide.
		s.d.Logger.Debug("admin check failed", "app", a.Name, "err", err)
	}
	view := setupTokenView{State: "waiting"}
	if !tok.IssuedAt.IsZero() {
		expires := tok.IssuedAt.Add(apps.SetupTokenLifetime)
		view.ExpiresAt = &expires
	}
	switch {
	case err == nil && created:
		view = setupTokenView{State: "created"}
	case tok.TimedOut || view.ExpiresAt != nil && s.d.Now().After(*view.ExpiresAt):
		view = setupTokenView{State: "locked"} // a restart prints a new token
	default:
		view.Token = tok.Token
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleJob(w http.ResponseWriter, r *http.Request, _ store.User) {
	job := r.PathValue("job")
	if !apps.ValidJob(job) {
		writeError(w, http.StatusNotFound, "request.not_found", "Unknown job", nil)
		return
	}
	var j apps.Job
	if err := s.d.Helper.Call(r.Context(), helper.OpAppJob, helper.JobArgs{Job: job}, &j); err != nil {
		s.respondHelperError(w, "apps.job", err)
		return
	}
	writeJSON(w, http.StatusOK, j)
}

func (s *Server) handleDeleteApp(w http.ResponseWriter, r *http.Request, user store.User) {
	a, ok := s.lookupApp(w, r)
	if !ok {
		return
	}
	var body struct {
		Confirm    string `json:"confirm"`
		DeleteData bool   `json:"deleteData"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Confirm != a.Name {
		writeError(w, http.StatusBadRequest, "validation.confirm", "Confirmation does not match the application name", nil)
		return
	}
	ctx, cancel := s.appsCtx(r, user, 6*time.Minute)
	defer cancel()
	if err := s.d.Helper.Call(ctx, helper.OpAppRemove, helper.AppRemoveArgs{Pool: a.Pool, Name: a.Name, DeleteData: body.DeleteData}, nil); err != nil {
		s.helperError(w, r, user, "app.remove", err)
		return
	}
	if err := s.d.Store.DeleteApp(ctx, a.Name); err != nil {
		s.internalError(w, "delete app record", err)
		return
	}
	detail := "data kept"
	if body.DeleteData {
		detail = "data deleted"
	}
	s.audit(r, user.Username, "app.remove", a.Name, "ok", detail)
	w.WriteHeader(http.StatusNoContent)
}

// handleEngineRemove uninstalls Docker. CWNSB applications with containers in
// the running Docker must be removed first; the helper also refuses while any
// other container exists. Applications without containers there stay
// recorded, data untouched: they come back with Docker on their pool (ADR-0035).
func (s *Server) handleEngineRemove(w http.ResponseWriter, r *http.Request, user store.User) {
	var body struct {
		Confirm    string `json:"confirm"`
		DeleteData bool   `json:"deleteData"`
		Pool       string `json:"pool"` // only to delete data kept by an earlier uninstall
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Confirm != "docker" {
		writeError(w, http.StatusBadRequest, "validation.confirm", `Type "docker" to confirm`, nil)
		return
	}
	recorded, err := s.d.Store.Apps(r.Context())
	if err != nil {
		s.internalError(w, "list apps", err)
		return
	}
	if len(recorded) > 0 {
		var st helper.AppsStatus
		if err := s.d.Helper.Call(r.Context(), helper.OpAppsStatus, nil, &st); err != nil {
			s.respondHelperError(w, "apps.status", err)
			return
		}
		if names := appsWithContainers(recorded, st.Projects); len(names) > 0 {
			writeError(w, http.StatusConflict, "apps.engine_in_use", "Remove the applications first", map[string]any{"apps": names})
			return
		}
	}
	ctx, cancel := s.appsCtx(r, user, 12*time.Minute)
	defer cancel()
	if err := s.d.Helper.Call(ctx, helper.OpEngineRemove, helper.EngineRemoveArgs{DeleteData: body.DeleteData, Pool: body.Pool}, nil); err != nil {
		s.helperError(w, r, user, "apps.engine_remove", err)
		return
	}
	detail := "data kept"
	if body.DeleteData {
		detail = "data deleted"
	}
	s.audit(r, user.Username, "apps.engine_remove", "", "ok", detail)
	writeJSON(w, http.StatusOK, map[string]bool{"removed": true})
}

// appsWithContainers names the recorded applications that have at least one
// container in the running Docker: only those hold Docker back (ADR-0035).
func appsWithContainers(recorded []store.App, projects []helper.ProjectView) []string {
	names := []string{}
	for _, a := range recorded {
		for _, p := range projects {
			if p.App == a.Name && len(p.Containers) > 0 {
				names = append(names, a.Name)
				break
			}
		}
	}
	return names
}
