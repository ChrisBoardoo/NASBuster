// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/crosswax/nasbuster/internal/helper"
	"github.com/crosswax/nasbuster/internal/shares"
	"github.com/crosswax/nasbuster/internal/store"
)

// shareView adds the path to a stored share.
type shareView struct {
	store.Share
	Path string `json:"path"`
}

func viewShare(s store.Share) shareView {
	return shareView{Share: s, Path: shares.SharePath(s.Pool, s.Name)}
}

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request, _ store.User) {
	users, err := s.d.Store.NASUsers(r.Context())
	if err != nil {
		s.internalError(w, "list NAS users", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request, admin store.User) {
	var body struct {
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if !shares.ValidUserName(body.Name) {
		writeError(w, http.StatusBadRequest, "validation.user_name", "Invalid user name", nil)
		return
	}
	if !shares.ValidPassword(body.Password) {
		writeError(w, http.StatusBadRequest, "validation.user_password", "Invalid password",
			map[string]any{"min": shares.PasswordMin, "max": shares.PasswordMax})
		return
	}
	if exists, err := s.d.Store.NASUserExists(r.Context(), body.Name); err != nil || exists {
		if err != nil {
			s.internalError(w, "check user", err)
			return
		}
		writeError(w, http.StatusConflict, "users.exists", "This user already exists", nil)
		return
	}
	ctx := helper.WithActor(r.Context(), admin.Username)
	if err := s.d.Helper.Call(ctx, helper.OpUserCreate, helper.UserArgs{Name: body.Name, Password: body.Password}, nil); err != nil {
		s.helperError(w, r, admin, "user.create", err)
		return
	}
	u, err := s.d.Store.CreateNASUser(r.Context(), body.Name)
	if err != nil {
		s.internalError(w, "record user", err)
		return
	}
	s.audit(r, admin.Username, "user.create", body.Name, "ok", "")
	writeJSON(w, http.StatusCreated, map[string]any{"user": u})
}

func (s *Server) handleUserPassword(w http.ResponseWriter, r *http.Request, admin store.User) {
	name := r.PathValue("name")
	var body struct {
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if !s.requireNASUser(w, r, name) {
		return
	}
	if !shares.ValidPassword(body.Password) {
		writeError(w, http.StatusBadRequest, "validation.user_password", "Invalid password",
			map[string]any{"min": shares.PasswordMin, "max": shares.PasswordMax})
		return
	}
	ctx := helper.WithActor(r.Context(), admin.Username)
	if err := s.d.Helper.Call(ctx, helper.OpUserSetPassword, helper.UserArgs{Name: name, Password: body.Password}, nil); err != nil {
		s.helperError(w, r, admin, "user.password", err)
		return
	}
	s.audit(r, admin.Username, "user.password", name, "ok", "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request, admin store.User) {
	name := r.PathValue("name")
	if !s.requireNASUser(w, r, name) {
		return
	}
	// 1. Remove the user from every share in Samba's configuration,
	// 2. then delete the account, 3. then forget it.
	all, err := s.d.Store.Shares(r.Context())
	if err != nil {
		s.internalError(w, "list shares", err)
		return
	}
	for i := range all {
		kept := all[i].Access[:0]
		for _, a := range all[i].Access {
			if a.User != name {
				kept = append(kept, a)
			}
		}
		all[i].Access = kept
	}
	ctx := helper.WithActor(r.Context(), admin.Username)
	if err := s.applySMB(ctx, all); err != nil {
		s.helperError(w, r, admin, "user.delete", err)
		return
	}
	if err := s.d.Helper.Call(ctx, helper.OpUserDelete, helper.UserArgs{Name: name}, nil); err != nil {
		s.helperError(w, r, admin, "user.delete", err)
		return
	}
	if err := s.d.Store.DeleteNASUser(r.Context(), name); err != nil {
		s.internalError(w, "forget user", err)
		return
	}
	s.audit(r, admin.Username, "user.delete", name, "ok", "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) requireNASUser(w http.ResponseWriter, r *http.Request, name string) bool {
	if !shares.ValidUserName(name) {
		writeError(w, http.StatusNotFound, "users.not_found", "User not found", nil)
		return false
	}
	exists, err := s.d.Store.NASUserExists(r.Context(), name)
	if err != nil {
		s.internalError(w, "check user", err)
		return false
	}
	if !exists {
		writeError(w, http.StatusNotFound, "users.not_found", "User not found", nil)
		return false
	}
	return true
}

func (s *Server) handleShares(w http.ResponseWriter, r *http.Request, _ store.User) {
	all, err := s.d.Store.Shares(r.Context())
	if err != nil {
		s.internalError(w, "list shares", err)
		return
	}
	out := make([]shareView, 0, len(all))
	for _, sh := range all {
		out = append(out, viewShare(sh))
	}
	writeJSON(w, http.StatusOK, map[string]any{"shares": out})
}

// validAccess checks an access list against recorded users.
func (s *Server) validAccess(w http.ResponseWriter, r *http.Request, access []store.ShareAccess) bool {
	seen := map[string]bool{}
	for _, a := range access {
		if a.Mode != shares.ReadWrite && a.Mode != shares.ReadOnly {
			writeError(w, http.StatusBadRequest, "validation.access_mode", "Mode must be rw or ro", nil)
			return false
		}
		if seen[a.User] {
			writeError(w, http.StatusBadRequest, "validation.access", "A user is listed twice", nil)
			return false
		}
		seen[a.User] = true
		if !s.requireNASUser(w, r, a.User) {
			return false
		}
	}
	return true
}

func (s *Server) handleCreateShare(w http.ResponseWriter, r *http.Request, admin store.User) {
	var body struct {
		Name              string              `json:"name"`
		Pool              string              `json:"pool"`
		Access            []store.ShareAccess `json:"access"`
		TimeMachine       bool                `json:"timeMachine"`
		TimeMachineMaxGiB int                 `json:"timeMachineMaxGiB"`
		Recycle           bool                `json:"recycle"`
		RecycleDays       int                 `json:"recycleDays"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if !shares.ValidShareName(body.Name) {
		writeError(w, http.StatusBadRequest, "validation.share_name", "Invalid share name", nil)
		return
	}
	if _, err := s.d.Store.PoolByName(r.Context(), body.Pool); err != nil {
		writeError(w, http.StatusNotFound, "storage.pool_not_found", "Pool not found", nil)
		return
	}
	if _, err := s.d.Store.ShareByName(r.Context(), body.Name); err == nil {
		writeError(w, http.StatusConflict, "shares.exists", "A share with this name exists", nil)
		return
	}
	if !s.validAccess(w, r, body.Access) {
		return
	}
	if !validTimeMachine(w, body.TimeMachine, body.TimeMachineMaxGiB) {
		return
	}
	if body.Recycle && body.RecycleDays == 0 {
		body.RecycleDays = 30
	}
	if !validRecycle(w, body.Recycle, body.RecycleDays) {
		return
	}
	ctx := helper.WithActor(r.Context(), admin.Username)
	if err := s.d.Helper.Call(ctx, helper.OpShareCreate, helper.ShareCreateArgs{Pool: body.Pool, Name: body.Name}, nil); err != nil {
		s.helperError(w, r, admin, "share.create", err)
		return
	}
	share := store.Share{Name: body.Name, Pool: body.Pool, Access: body.Access, TimeMachine: body.TimeMachine, TimeMachineMaxGiB: body.TimeMachineMaxGiB,
		Recycle: body.Recycle, RecycleDays: body.RecycleDays}
	if !s.applyOrFail(w, r, admin, "share.create", func(all []store.Share) []store.Share { return append(all, share) }) {
		return // nothing recorded; the empty subvolume is adopted on retry
	}
	created, err := s.d.Store.CreateShare(r.Context(), share)
	if err != nil {
		s.internalError(w, "record share", err)
		return
	}
	s.audit(r, admin.Username, "share.create", body.Name, "ok", body.Pool)
	writeJSON(w, http.StatusCreated, map[string]any{"share": viewShare(created)})
}

func (s *Server) handleShareAccess(w http.ResponseWriter, r *http.Request, admin store.User) {
	name := r.PathValue("name")
	var body struct {
		Access []store.ShareAccess `json:"access"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if !shares.ValidShareName(name) {
		writeError(w, http.StatusNotFound, "shares.not_found", "Share not found", nil)
		return
	}
	if !s.validAccess(w, r, body.Access) {
		return
	}
	if _, err := s.d.Store.ShareByName(r.Context(), name); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "shares.not_found", "Share not found", nil)
		return
	} else if err != nil {
		s.internalError(w, "load share", err)
		return
	}
	withAccess := func(all []store.Share) []store.Share {
		for i := range all {
			if all[i].Name == name {
				all[i].Access = body.Access
			}
		}
		return all
	}
	if !s.applyOrFail(w, r, admin, "share.access", withAccess) {
		return // Samba and the database still agree on the previous access
	}
	if err := s.d.Store.SetShareAccess(r.Context(), name, body.Access); err != nil {
		s.internalError(w, "set access", err)
		return
	}
	updated, _ := s.d.Store.ShareByName(r.Context(), name)
	s.audit(r, admin.Username, "share.access", name, "ok", "")
	writeJSON(w, http.StatusOK, map[string]any{"share": viewShare(updated)})
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request, admin store.User) {
	var res helper.SMBSessionsResult
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if err := s.d.Helper.Call(ctx, helper.OpSMBSessions, nil, &res); err != nil {
		s.respondHelperError(w, "smb.sessions", err) // read-only and polled: never audited
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// applyOrFail pushes the complete desired state to Samba (via the helper).
// change, if set, edits the recorded shares in memory first: the database is
// only updated by the caller once Samba accepted the new state.
func (s *Server) applyOrFail(w http.ResponseWriter, r *http.Request, admin store.User, action string, change ...func([]store.Share) []store.Share) bool {
	all, err := s.d.Store.Shares(r.Context())
	if err != nil {
		s.internalError(w, "list shares", err)
		return false
	}
	for _, c := range change {
		all = c(all)
	}
	if err := s.applySMB(helper.WithActor(r.Context(), admin.Username), all); err != nil {
		s.helperError(w, r, admin, action, err)
		return false
	}
	return true
}

func (s *Server) applySMB(ctx context.Context, all []store.Share) error {
	desired := make([]shares.Share, 0, len(all))
	for _, sh := range all {
		access := make([]shares.Access, 0, len(sh.Access))
		for _, a := range sh.Access {
			access = append(access, shares.Access{User: a.User, Mode: a.Mode})
		}
		desired = append(desired, shares.Share{Name: sh.Name, Pool: sh.Pool, Access: access,
			TimeMachine: sh.TimeMachine, TimeMachineMaxGiB: sh.TimeMachineMaxGiB, Recycle: sh.Recycle, RecycleDays: sh.RecycleDays})
	}
	return s.d.Helper.Call(ctx, helper.OpSMBApply, helper.SMBApplyArgs{Shares: desired}, nil)
}

// ReconcileShares pushes the recorded shares to Samba (desired state, ADR-0005).
// nasbusterd calls it at startup: right after installation it replaces the
// distribution's default smb.conf (which allows guests) with CWNSB's.
func ReconcileShares(ctx context.Context, st *store.Store, hc helper.Client) error {
	all, err := st.Shares(ctx)
	if err != nil {
		return err
	}
	s := &Server{d: Deps{Store: st, Helper: hc}}
	return s.applySMB(helper.WithActor(ctx, "startup"), all)
}

// handleDeleteShare deletes a share AND its files and snapshots. Order:
// Samba first (nobody can open it any more), then the data, then the record.
func (s *Server) handleDeleteShare(w http.ResponseWriter, r *http.Request, admin store.User) {
	name := r.PathValue("name")
	var body struct {
		Confirm string `json:"confirm"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if !shares.ValidShareName(name) {
		writeError(w, http.StatusNotFound, "shares.not_found", "Share not found", nil)
		return
	}
	sh, err := s.d.Store.ShareByName(r.Context(), name)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "shares.not_found", "Share not found", nil)
		return
	}
	if err != nil {
		s.internalError(w, "load share", err)
		return
	}
	if body.Confirm != name {
		writeError(w, http.StatusBadRequest, "validation.confirm", "Confirmation does not match the share name", nil)
		return
	}
	without := func(all []store.Share) []store.Share {
		return slices.DeleteFunc(all, func(x store.Share) bool { return x.Name == name })
	}
	if !s.applyOrFail(w, r, admin, "share.delete", without) {
		return
	}
	ctx := helper.WithActor(context.WithoutCancel(r.Context()), admin.Username)
	if err := s.d.Helper.Call(ctx, helper.OpShareDelete, helper.ShareCreateArgs{Pool: sh.Pool, Name: name}, nil); err != nil {
		if all, lerr := s.d.Store.Shares(ctx); lerr == nil {
			_ = s.applySMB(ctx, all) // put the share back in Samba: its data is still there
		}
		s.helperError(w, r, admin, "share.delete", err)
		return
	}
	if err := s.d.Store.DeleteShare(ctx, name); err != nil {
		s.internalError(w, "delete share record", err)
		return
	}
	s.audit(r, admin.Username, "share.delete", name, "ok", sh.Pool)
	w.WriteHeader(http.StatusNoContent)
}

func validTimeMachine(w http.ResponseWriter, enabled bool, maxGiB int) bool {
	if maxGiB < 0 || maxGiB > shares.MaxTimeMachineGiB || (!enabled && maxGiB != 0) {
		writeError(w, http.StatusBadRequest, "validation.time_machine", "Invalid Time Machine size limit", nil)
		return false
	}
	return true
}

// handleShareTimeMachine turns a share into a Time Machine destination (or
// back). Samba first, then the database.
func (s *Server) handleShareTimeMachine(w http.ResponseWriter, r *http.Request, admin store.User) {
	name := r.PathValue("name")
	var body struct {
		Enabled bool `json:"enabled"`
		MaxGiB  int  `json:"maxSizeGiB"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if !shares.ValidShareName(name) {
		writeError(w, http.StatusNotFound, "shares.not_found", "Share not found", nil)
		return
	}
	if !validTimeMachine(w, body.Enabled, body.MaxGiB) {
		return
	}
	if _, err := s.d.Store.ShareByName(r.Context(), name); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "shares.not_found", "Share not found", nil)
		return
	} else if err != nil {
		s.internalError(w, "load share", err)
		return
	}
	with := func(all []store.Share) []store.Share {
		for i := range all {
			if all[i].Name == name {
				all[i].TimeMachine, all[i].TimeMachineMaxGiB = body.Enabled, body.MaxGiB
			}
		}
		return all
	}
	if !s.applyOrFail(w, r, admin, "share.time_machine", with) {
		return
	}
	if err := s.d.Store.SetShareTimeMachine(r.Context(), name, body.Enabled, body.MaxGiB); err != nil {
		s.internalError(w, "set time machine", err)
		return
	}
	updated, _ := s.d.Store.ShareByName(r.Context(), name)
	detail := "off"
	if body.Enabled {
		detail = "on"
	}
	s.audit(r, admin.Username, "share.time_machine", name, "ok", detail)
	writeJSON(w, http.StatusOK, map[string]any{"share": viewShare(updated)})
}

func validRecycle(w http.ResponseWriter, enabled bool, days int) bool {
	if (enabled && (days < 1 || days > 365)) || (!enabled && days != 0) {
		writeError(w, http.StatusBadRequest, "validation.recycle", "The recycle bin keeps files 1 to 365 days", nil)
		return false
	}
	return true
}

// handleShareRecycle turns a share's recycle bin on or off.
func (s *Server) handleShareRecycle(w http.ResponseWriter, r *http.Request, admin store.User) {
	name := r.PathValue("name")
	var body struct {
		Enabled bool `json:"enabled"`
		Days    int  `json:"days"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Enabled && body.Days == 0 {
		body.Days = 30
	}
	if !shares.ValidShareName(name) {
		writeError(w, http.StatusNotFound, "shares.not_found", "Share not found", nil)
		return
	}
	if !validRecycle(w, body.Enabled, body.Days) {
		return
	}
	if _, err := s.d.Store.ShareByName(r.Context(), name); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "shares.not_found", "Share not found", nil)
		return
	} else if err != nil {
		s.internalError(w, "load share", err)
		return
	}
	with := func(all []store.Share) []store.Share {
		for i := range all {
			if all[i].Name == name {
				all[i].Recycle, all[i].RecycleDays = body.Enabled, body.Days
			}
		}
		return all
	}
	if !s.applyOrFail(w, r, admin, "share.recycle", with) {
		return
	}
	if err := s.d.Store.SetShareRecycle(r.Context(), name, body.Enabled, body.Days); err != nil {
		s.internalError(w, "set recycle", err)
		return
	}
	updated, _ := s.d.Store.ShareByName(r.Context(), name)
	detail := "off"
	if body.Enabled {
		detail = strconv.Itoa(body.Days) + " days"
	}
	s.audit(r, admin.Username, "share.recycle", name, "ok", detail)
	writeJSON(w, http.StatusOK, map[string]any{"share": viewShare(updated)})
}
