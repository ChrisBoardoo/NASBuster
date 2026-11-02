// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"errors"
	"net/http"

	"github.com/crosswax/nasbuster/internal/helper"
	"github.com/crosswax/nasbuster/internal/scheduler"
	"github.com/crosswax/nasbuster/internal/shares"
	"github.com/crosswax/nasbuster/internal/snapshots"
	"github.com/crosswax/nasbuster/internal/store"
)

func (s *Server) handleGetProtection(w http.ResponseWriter, r *http.Request, _ store.User) {
	p, err := scheduler.LoadProtection(r.Context(), s.d.Store)
	if err != nil {
		s.internalError(w, "load protection", err)
		return
	}
	state, err := scheduler.LoadState(r.Context(), s.d.Store)
	if err != nil {
		s.internalError(w, "load scheduler state", err)
		return
	}
	// The SMART history and alert timers stay internal.
	view := map[string]any{"lastSnapshotRun": state.LastSnapshotRun, "lastScrub": state.LastScrub}
	writeJSON(w, http.StatusOK, map[string]any{"protection": p, "state": view})
}

func (s *Server) handlePutProtection(w http.ResponseWriter, r *http.Request, user store.User) {
	var p scheduler.Protection
	if !decodeJSON(w, r, &p) {
		return
	}
	if !p.Valid() {
		writeError(w, http.StatusBadRequest, "validation.protection", "Retention or scrub interval out of range", nil)
		return
	}
	if err := s.d.Store.SetSetting(r.Context(), scheduler.ProtectionKey, p); err != nil {
		s.internalError(w, "save protection", err)
		return
	}
	s.audit(r, user.Username, "protection.update", "", "ok", "")
	writeJSON(w, http.StatusOK, map[string]any{"protection": p})
}

// snapshotView is a snapshot with its time decoded.
type snapshotView struct {
	Name string `json:"name"`
	Time int64  `json:"time"`
}

func (s *Server) lookupShare(w http.ResponseWriter, r *http.Request) (store.Share, bool) {
	name := r.PathValue("name")
	if !shares.ValidShareName(name) {
		writeError(w, http.StatusNotFound, "shares.not_found", "Share not found", nil)
		return store.Share{}, false
	}
	sh, err := s.d.Store.ShareByName(r.Context(), name)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "shares.not_found", "Share not found", nil)
		return store.Share{}, false
	}
	if err != nil {
		s.internalError(w, "load share", err)
		return store.Share{}, false
	}
	return sh, true
}

func (s *Server) handleSnapshots(w http.ResponseWriter, r *http.Request, user store.User) {
	sh, ok := s.lookupShare(w, r)
	if !ok {
		return
	}
	var res helper.SnapshotListResult
	ctx := helper.WithActor(r.Context(), user.Username)
	if err := s.d.Helper.Call(ctx, helper.OpSnapshotList, helper.SnapshotArgs{Pool: sh.Pool, Share: sh.Name}, &res); err != nil {
		s.helperError(w, r, user, "snapshot.list", err)
		return
	}
	out := make([]snapshotView, 0, len(res.Snapshots))
	for _, n := range res.Snapshots {
		t, _ := snapshots.Parse(n)
		out = append(out, snapshotView{Name: n, Time: t.Unix()})
	}
	writeJSON(w, http.StatusOK, map[string]any{"snapshots": out})
}

func (s *Server) handleCreateSnapshot(w http.ResponseWriter, r *http.Request, user store.User) {
	sh, ok := s.lookupShare(w, r)
	if !ok {
		return
	}
	var res map[string]string
	ctx := helper.WithActor(r.Context(), user.Username)
	if err := s.d.Helper.Call(ctx, helper.OpSnapshotCreate, helper.SnapshotArgs{Pool: sh.Pool, Share: sh.Name}, &res); err != nil {
		s.helperError(w, r, user, "snapshot.create", err)
		return
	}
	t, _ := snapshots.Parse(res["name"])
	s.audit(r, user.Username, "snapshot.create", sh.Name, "ok", res["name"])
	writeJSON(w, http.StatusCreated, map[string]any{"snapshot": snapshotView{Name: res["name"], Time: t.Unix()}})
}

func (s *Server) handleDeleteSnapshot(w http.ResponseWriter, r *http.Request, user store.User) {
	sh, ok := s.lookupShare(w, r)
	if !ok {
		return
	}
	name := r.PathValue("snapshot")
	if !snapshots.ValidName(name) {
		writeError(w, http.StatusNotFound, "snapshots.not_found", "Snapshot not found", nil)
		return
	}
	ctx := helper.WithActor(r.Context(), user.Username)
	if err := s.d.Helper.Call(ctx, helper.OpSnapshotDelete, helper.SnapshotArgs{Pool: sh.Pool, Share: sh.Name, Name: name}, nil); err != nil {
		s.helperError(w, r, user, "snapshot.delete", err)
		return
	}
	s.audit(r, user.Username, "snapshot.delete", sh.Name, "ok", name)
	w.WriteHeader(http.StatusNoContent)
}
