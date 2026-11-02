// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/crosswax/nasbuster/internal/helper"
	"github.com/crosswax/nasbuster/internal/resync"
	"github.com/crosswax/nasbuster/internal/scheduler"
	"github.com/crosswax/nasbuster/internal/storage"
	"github.com/crosswax/nasbuster/internal/store"
	"github.com/crosswax/nasbuster/internal/validate"
)

// Disk failure handling (ADR-0012). The helper decides what is safe; the
// daemon checks what it knows better (the recorded members) and keeps its
// records in step once the helper has acted.

// repairCtx outlives the HTTP request: once the helper acts, the daemon must
// record the result even if the browser goes away.
func repairCtx(r *http.Request, user store.User) (context.Context, context.CancelFunc) {
	ctx := helper.WithActor(context.WithoutCancel(r.Context()), user.Username)
	return context.WithTimeout(ctx, 3*time.Minute)
}

func (s *Server) handleMountDegraded(w http.ResponseWriter, r *http.Request, user store.User) {
	p, ok := s.lookupPool(w, r)
	if !ok {
		return
	}
	var body struct {
		Confirm string `json:"confirm"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	ctx, cancel := repairCtx(r, user)
	defer cancel()
	before := s.d.Now()
	if err := s.d.Helper.Call(ctx, helper.OpPoolMountDegraded, helper.PoolConfirmArgs{Name: p.Name, Confirm: body.Confirm}, nil); err != nil {
		s.helperError(w, r, user, "pool.mount_degraded", err)
		return
	}
	// What is written now has one copy: a scrub is owed once every disk is back (ADR-0027).
	if err := resync.Mark(ctx, s.d.Store, p.Name, before); err != nil {
		s.d.Logger.Warn("resync mark", "pool", p.Name, "err", err)
	}
	s.audit(r, user.Username, "pool.mount_degraded", p.Name, "ok", "")
	writeJSON(w, http.StatusOK, map[string]any{"pool": s.poolWithStatus(ctx, p)})
}

func (s *Server) handleReplaceDisk(w http.ResponseWriter, r *http.Request, user store.User) {
	p, ok := s.lookupPool(w, r)
	if !ok {
		return
	}
	var body struct {
		Old     string `json:"old"`
		New     string `json:"new"`
		Confirm string `json:"confirm"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if !validate.DiskID(body.Old) || !validate.DiskID(body.New) {
		writeError(w, http.StatusBadRequest, "validation.disk_id", "Invalid disk id", nil)
		return
	}
	if !slices.Contains(p.Members, body.Old) {
		// Just replaced (members updated already) and still rebuilding: busy.
		if v := s.poolWithStatus(r.Context(), p); v.Status != nil &&
			((v.Status.Replace != nil && v.Status.Replace.State == "running") || (v.Status.MD != nil && v.Status.MD.Sync != nil)) {
			writeError(w, http.StatusConflict, helper.CodePoolBusy, "A replacement is already running", nil)
			return
		}
		writeError(w, http.StatusConflict, helper.CodeMemberNotFound, "This disk is not a member of the pool", map[string]any{"disk": body.Old})
		return
	}
	if slices.Contains(p.Members, body.New) {
		writeError(w, http.StatusConflict, helper.CodeDiskNotAvailable, "This disk is already a member of the pool", map[string]any{"disk": body.New})
		return
	}
	ctx, cancel := repairCtx(r, user)
	defer cancel()
	var res helper.PoolReplaceResult
	args := helper.PoolReplaceArgs{Name: p.Name, Old: body.Old, New: body.New, Confirm: body.Confirm}
	if err := s.d.Helper.Call(ctx, helper.OpPoolReplace, args, &res); err != nil {
		s.helperError(w, r, user, "pool.replace", err)
		return
	}
	s.invalidateDisks()
	members := slices.Clone(p.Members)
	members[slices.Index(members, body.Old)] = body.New
	if err := s.d.Store.SetPoolMembers(ctx, p.Name, members); err != nil {
		// The rebuild runs anyway; the record is fixed by hand or by the next replace.
		s.d.Logger.Error("record replaced member", "pool", p.Name, "err", err)
	}
	s.audit(r, user.Username, "pool.replace", p.Name, "ok", body.Old+" -> "+body.New)
	writeJSON(w, http.StatusAccepted, map[string]any{"replace": res})
}

func (s *Server) handleRestoreRedundancy(w http.ResponseWriter, r *http.Request, user store.User) {
	p, ok := s.lookupPool(w, r)
	if !ok {
		return
	}
	ctx, cancel := repairCtx(r, user)
	defer cancel()
	var res helper.RestoreResult
	if err := s.d.Helper.Call(ctx, helper.OpPoolRestore, helper.PoolArgs{Name: p.Name}, &res); err != nil {
		s.helperError(w, r, user, "pool.restore_redundancy", err)
		return
	}
	s.audit(r, user.Username, "pool.restore_redundancy", p.Name, "ok", "")
	writeJSON(w, http.StatusAccepted, map[string]any{"restore": res})
}

func (s *Server) handleClearErrors(w http.ResponseWriter, r *http.Request, user store.User) {
	p, ok := s.lookupPool(w, r)
	if !ok {
		return
	}
	ctx, cancel := repairCtx(r, user)
	defer cancel()
	if err := s.d.Helper.Call(ctx, helper.OpPoolClearErrors, helper.PoolArgs{Name: p.Name}, nil); err != nil {
		s.helperError(w, r, user, "pool.clear_errors", err)
		return
	}
	s.audit(r, user.Username, "pool.clear_errors", p.Name, "ok", "")
	writeJSON(w, http.StatusOK, map[string]bool{"cleared": true})
}

// handleDemoFault lets the UI be built against disk failures in demo mode.
// The route does not exist outside demo mode.
func (s *Server) handleDemoFault(w http.ResponseWriter, r *http.Request, user store.User) {
	var body struct {
		Kind string `json:"kind"` // unplug, replug, errors, foreign, usbkey, residual
		Disk string `json:"disk"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if !slices.Contains([]string{"unplug", "replug", "errors", "foreign", "usbkey", "residual"}, body.Kind) || !validate.DiskID(body.Disk) {
		writeError(w, http.StatusBadRequest, "validation.fault", "kind must be unplug, replug, errors, foreign, usbkey or residual, with a disk id", nil)
		return
	}
	if err := s.d.DemoFault(body.Kind, body.Disk); err != nil {
		writeError(w, http.StatusNotFound, "storage.disk_not_found", err.Error(), nil)
		return
	}
	s.invalidateDisks()
	s.audit(r, user.Username, "demo.fault", body.Disk, "ok", body.Kind)
	w.WriteHeader(http.StatusNoContent)
}

// handleWipeDisk erases a disk that is not in use (pre-formatted or retired),
// so that it can join a pool. The helper decides what may be wiped.
func (s *Server) handleWipeDisk(w http.ResponseWriter, r *http.Request, user store.User) {
	id := r.PathValue("id")
	if !validate.DiskID(id) {
		writeError(w, http.StatusNotFound, "storage.disk_not_found", "Disk not found", nil)
		return
	}
	var body struct {
		Confirm string `json:"confirm"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	ctx, cancel := repairCtx(r, user)
	defer cancel()
	if err := s.d.Helper.Call(ctx, helper.OpDiskWipe, helper.DiskWipeArgs{ID: id, Confirm: body.Confirm}, nil); err != nil {
		s.helperError(w, r, user, "disk.wipe", err)
		return
	}
	s.invalidateDisks()
	s.audit(r, user.Username, "disk.wipe", id, "ok", "")
	writeJSON(w, http.StatusOK, map[string]bool{"wiped": true})
}

// handleDestroyPool destroys an EMPTY pool (or forgets one whose disks are
// all gone). Shares must be deleted first: data is only ever deleted share
// by share, with their own confirmation.
func (s *Server) handleDestroyPool(w http.ResponseWriter, r *http.Request, user store.User) {
	p, ok := s.lookupPool(w, r)
	if !ok {
		return
	}
	var body struct {
		Confirm string `json:"confirm"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	all, err := s.d.Store.Shares(r.Context())
	if err != nil {
		s.internalError(w, "list shares", err)
		return
	}
	var names []string
	for _, sh := range all {
		if sh.Pool == p.Name {
			names = append(names, sh.Name)
		}
	}
	if len(names) > 0 {
		writeError(w, http.StatusConflict, "storage.pool_has_shares", "Delete the pool's shares first", map[string]any{"shares": names})
		return
	}
	ctx, cancel := repairCtx(r, user)
	defer cancel()
	var res helper.PoolDestroyResult
	if err := s.d.Helper.Call(ctx, helper.OpPoolDestroy, helper.PoolConfirmArgs{Name: p.Name, Confirm: body.Confirm}, &res); err != nil {
		s.helperError(w, r, user, "pool.destroy", err)
		return
	}
	s.invalidateDisks()
	if err := s.d.Store.DeletePool(ctx, p.Name); err != nil {
		s.internalError(w, "delete pool record", err)
		return
	}
	detail := "destroyed"
	if res.Forgotten {
		detail = "forgotten (disks gone)"
	}
	if err := resync.Forget(r.Context(), s.d.Store, p.Name); err != nil {
		s.d.Logger.Warn("resync mark", "pool", p.Name, "err", err)
	}
	s.audit(r, user.Username, "pool.destroy", p.Name, "ok", detail)
	writeJSON(w, http.StatusOK, map[string]any{"result": res})
}

// handleResume takes back a disk that was unplugged and came back (ADR-0027):
// the pool's shares are closed, the pool is remounted with every disk, then a
// resync job converts the single-copy chunks and scrubs. Never automatic: the
// remount cuts the pool's SMB connections and restarts Docker if it lives there.
func (s *Server) handleResume(w http.ResponseWriter, r *http.Request, user store.User) {
	p, ok := s.lookupPool(w, r)
	if !ok {
		return
	}
	var body struct {
		Confirm string `json:"confirm"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	all, err := s.d.Store.Shares(r.Context())
	if err != nil {
		s.internalError(w, "list shares", err)
		return
	}
	shareNames := []string{}
	for _, sh := range all {
		if sh.Pool == p.Name {
			shareNames = append(shareNames, sh.Name)
		}
	}
	ctx, cancel := repairCtx(r, user)
	defer cancel()
	// Taken before the call: the resync scrub it starts must clear the mark.
	before := s.d.Now()
	var res helper.PoolResumeResult
	err = s.d.Helper.Call(ctx, helper.OpPoolResume, helper.PoolResumeArgs{Name: p.Name, Confirm: body.Confirm, Shares: shareNames}, &res)
	if res.Remounted || res.ResyncStarted {
		if merr := resync.Mark(ctx, s.d.Store, p.Name, before); merr != nil {
			s.d.Logger.Warn("resync mark", "pool", p.Name, "err", merr)
		}
	}
	if err != nil {
		s.helperError(w, r, user, "pool.resume", err)
		return
	}
	s.audit(r, user.Username, "pool.resume", p.Name, "ok", "")
	writeJSON(w, http.StatusAccepted, map[string]any{"resume": res, "pool": s.poolWithStatus(ctx, p)})
}

// handleReassemble restarts a parity pool that lost more disks than it
// tolerates, once they are all back: forced assembly, parity repair, then the
// scrub that the resync mark asks for (ADR-0032).
func (s *Server) handleReassemble(w http.ResponseWriter, r *http.Request, user store.User) {
	p, ok := s.lookupPool(w, r)
	if !ok {
		return
	}
	var body struct {
		Confirm string `json:"confirm"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	all, err := s.d.Store.Shares(r.Context())
	if err != nil {
		s.internalError(w, "list shares", err)
		return
	}
	shareNames := []string{}
	for _, sh := range all {
		if sh.Pool == p.Name {
			shareNames = append(shareNames, sh.Name)
		}
	}
	// The unmount, the assembly and the mount may each wait 2 minutes.
	ctx, cancel := context.WithTimeout(helper.WithActor(context.WithoutCancel(r.Context()), user.Username), 8*time.Minute)
	defer cancel()
	// Seen before acting: a pool beyond its tolerance owes a parity repair,
	// then a scrub (the scheduler runs them). Recorded before anything is
	// forced, so that a failure halfway, a timeout or a restart of the daemon
	// cannot lose them; nothing is forced if they cannot be recorded.
	var st storage.PoolStatus
	if err := s.d.Helper.Call(ctx, helper.OpPoolStatus, helper.PoolArgs{Name: p.Name}, &st); err != nil {
		s.helperError(w, r, user, "pool.reassemble", err)
		return
	}
	if err := resync.Apply(ctx, s.d.Store, &st, s.d.Now(), s.bootID()); err != nil {
		s.internalError(w, "record the parity repair owed", err)
		return
	}
	var res helper.PoolReassembleResult
	err = s.d.Helper.Call(ctx, helper.OpPoolReassemble, helper.PoolReassembleArgs{Name: p.Name, Confirm: body.Confirm, Shares: shareNames}, &res)
	if err != nil {
		s.helperError(w, r, user, "pool.reassemble", err)
		return
	}
	s.audit(r, user.Username, "pool.reassemble", p.Name, "ok", strings.Join(res.Members, " "))
	writeJSON(w, http.StatusAccepted, map[string]any{"reassemble": res, "pool": s.poolWithStatus(ctx, p)})
}

// handleImportable lists CWNSB pools found on disks this NAS does not know
// (after a reinstall, or disks moved from another CWNSB NAS).
func (s *Server) handleImportable(w http.ResponseWriter, r *http.Request, _ store.User) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	var scan helper.ImportScan
	if err := s.d.Helper.Call(ctx, helper.OpPoolsScan, nil, &scan); err != nil {
		s.respondHelperError(w, "pool.scan", err)
		return
	}
	writeJSON(w, http.StatusOK, scan)
}

// handleImportPool adopts a complete pool found by the scan: nothing is
// written to its disks; it is assembled, mounted and recorded.
func (s *Server) handleImportPool(w http.ResponseWriter, r *http.Request, user store.User) {
	var body struct {
		Name    string `json:"name"`
		Confirm string `json:"confirm"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if _, err := s.d.Store.PoolByName(r.Context(), body.Name); err == nil {
		writeError(w, http.StatusConflict, helper.CodePoolExists, "A pool with this name exists", nil)
		return
	}
	ctx, cancel := repairCtx(r, user)
	defer cancel()
	var res helper.PoolCreateResult
	if err := s.d.Helper.Call(ctx, helper.OpPoolImport, helper.PoolConfirmArgs{Name: body.Name, Confirm: body.Confirm}, &res); err != nil {
		s.helperError(w, r, user, "pool.import", err)
		return
	}
	s.invalidateDisks()
	p, err := s.d.Store.CreatePool(ctx, store.Pool{Name: res.Name, Layout: res.Layout, UUID: res.UUID, MDDevice: res.MDDevice, Members: res.Members})
	if err != nil {
		s.internalError(w, "record imported pool", err)
		return
	}
	s.audit(r, user.Username, "pool.import", res.Name, "ok", res.Layout)
	writeJSON(w, http.StatusCreated, map[string]any{"pool": s.poolWithStatus(ctx, p)})
}

// handleConfigBackup stores the configuration on every mounted pool now.
func (s *Server) handleConfigBackup(w http.ResponseWriter, r *http.Request, user store.User) {
	if s.d.ExportPath == "" {
		writeError(w, http.StatusServiceUnavailable, "backup.unavailable", "Configuration backups are not set up", nil)
		return
	}
	ctx, cancel := repairCtx(r, user)
	defer cancel()
	args, err := scheduler.BackupArgs(ctx, s.d.Store)
	if err != nil {
		s.internalError(w, "describe configuration", err)
		return
	}
	if err := s.d.Store.ExportTo(ctx, s.d.ExportPath); err != nil {
		s.internalError(w, "export database", err)
		return
	}
	var res helper.BackupResult
	if err := s.d.Helper.Call(ctx, helper.OpConfigBackup, args, &res); err != nil {
		s.helperError(w, r, user, "config.backup", err)
		return
	}
	if len(res.Pools) == 0 {
		writeError(w, http.StatusConflict, "backup.no_pool", "No mounted pool to store the backup on", nil)
		return
	}
	s.audit(r, user.Username, "config.backup", res.Name, "ok", strings.Join(res.Pools, ", "))
	writeJSON(w, http.StatusCreated, res)
}

// handleConfigBackups lists the configuration backups found on the pools.
func (s *Server) handleConfigBackups(w http.ResponseWriter, r *http.Request, _ store.User) {
	var list helper.BackupList
	if err := s.d.Helper.Call(r.Context(), helper.OpConfigBackups, nil, &list); err != nil {
		s.respondHelperError(w, "config.list", err)
		return
	}
	// Which backups this installation made (P6): the others come from the
	// installation a restore would bring back.
	id, err := s.d.Store.InstallationID(r.Context())
	if err != nil {
		s.internalError(w, "installation id", err)
		return
	}
	for i := range list.Backups {
		list.Backups[i].ThisInstallation = list.Backups[i].Installation == id
	}
	writeJSON(w, http.StatusOK, list)
}

// handleConfigRestore restores a configuration backup: the helper recreates
// the NAS accounts (same IDs) and Samba passwords and leaves the database;
// the daemon then restarts to load it. Every session ends, including this one:
// the administrator logs in again with the password of the backup.
func (s *Server) handleConfigRestore(w http.ResponseWriter, r *http.Request, user store.User) {
	var body struct {
		Pool    string `json:"pool"`
		Name    string `json:"name"`
		Confirm string `json:"confirm"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Confirm != "restore" {
		writeError(w, http.StatusBadRequest, "validation.confirm", `Type "restore" to confirm`, nil)
		return
	}
	if s.d.Restart == nil {
		writeError(w, http.StatusServiceUnavailable, "backup.unavailable", "Restoring is not supported here", nil)
		return
	}
	ctx, cancel := repairCtx(r, user)
	defer cancel()
	var res helper.RestoreConfigResult
	if err := s.d.Helper.Call(ctx, helper.OpConfigRestore, helper.RestoreArgs{Pool: body.Pool, Name: body.Name}, &res); err != nil {
		s.helperError(w, r, user, "config.restore", err)
		return
	}
	s.audit(r, user.Username, "config.restore", body.Pool+"/"+body.Name, "ok", strconv.Itoa(len(res.Users))+" users, "+strconv.Itoa(len(res.Conflicts))+" conflicts")
	writeJSON(w, http.StatusAccepted, res)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	go s.d.Restart()
}

// handleLocateDisk makes a disk's activity LED blink for 30 seconds.
func (s *Server) handleLocateDisk(w http.ResponseWriter, r *http.Request, user store.User) {
	id := r.PathValue("id")
	if !validate.DiskID(id) {
		writeError(w, http.StatusNotFound, "storage.disk_not_found", "Disk not found", nil)
		return
	}
	var out map[string]int
	if err := s.d.Helper.Call(helper.WithActor(r.Context(), user.Username), helper.OpDiskLocate, helper.DiskSmartArgs{ID: id}, &out); err != nil {
		s.respondHelperError(w, "disk.locate", err)
		return
	}
	writeJSON(w, http.StatusAccepted, out)
}
