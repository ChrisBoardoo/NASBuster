// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/crosswax/nasbuster/internal/helper"
	"github.com/crosswax/nasbuster/internal/resync"
	"github.com/crosswax/nasbuster/internal/storage"
	"github.com/crosswax/nasbuster/internal/store"
	"github.com/crosswax/nasbuster/internal/validate"
)

// layoutView describes a layout to the UI.
type layoutView struct {
	Name        string `json:"name"`
	MinDisks    int    `json:"minDisks"`
	MaxDisks    int    `json:"maxDisks"` // 0 = no limit
	Tolerates   int    `json:"tolerates"`
	SelfHealing bool   `json:"selfHealing"`
	Parity      bool   `json:"parity"`
}

// poolView is a pool with its live status (null if the helper cannot be reached).
type poolView struct {
	store.Pool
	Status      *storage.PoolStatus `json:"status"`
	StatusError string              `json:"statusError"`
}

func (s *Server) handleLayouts(w http.ResponseWriter, r *http.Request, _ store.User) {
	out := []layoutView{}
	for _, l := range storage.Layouts() {
		out = append(out, layoutView{Name: l.Name, MinDisks: l.MinDisks, MaxDisks: l.MaxDisks,
			Tolerates: l.Tolerates, SelfHealing: l.SelfHealing, Parity: l.MDLevel > 0})
	}
	writeJSON(w, http.StatusOK, map[string]any{"layouts": out})
}

// handlePoolPlan previews a pool. It changes nothing.
func (s *Server) handlePoolPlan(w http.ResponseWriter, r *http.Request, _ store.User) {
	var body struct {
		Layout string   `json:"layout"`
		Disks  []string `json:"disks"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if _, ok := storage.LayoutByName(body.Layout); !ok {
		writeError(w, http.StatusBadRequest, "validation.layout", "Unknown layout", nil)
		return
	}
	sizes, ok := s.availableDiskSizes(w, r, body.Disks)
	if !ok {
		return
	}
	plan, err := storage.PlanPool(body.Layout, sizes)
	if !s.planError(w, body.Layout, err) {
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

// availableDiskSizes resolves disk ids and checks they are blank (as the
// daemon sees them; the helper checks again, at a lower level, before writing).
func (s *Server) availableDiskSizes(w http.ResponseWriter, r *http.Request, ids []string) ([]uint64, bool) {
	if len(ids) > 16 {
		writeError(w, http.StatusBadRequest, "validation.disk_count", "Too many disks", nil)
		return nil, false
	}
	seen := map[string]bool{}
	sizes := make([]uint64, 0, len(ids))
	for _, id := range ids {
		if !validate.DiskID(id) || seen[id] {
			writeError(w, http.StatusBadRequest, "validation.disks", "Invalid or repeated disk id", nil)
			return nil, false
		}
		seen[id] = true
		d, err := s.d.Disks.Disk(r.Context(), id)
		if err != nil {
			s.storageError(w, err)
			return nil, false
		}
		if d.Role != storage.RoleAvailable {
			writeError(w, http.StatusConflict, "storage.disk_not_available", "Only blank disks can be used",
				map[string]any{"disk": id, "role": d.Role})
			return nil, false
		}
		sizes = append(sizes, d.SizeBytes)
	}
	return sizes, true
}

func (s *Server) planError(w http.ResponseWriter, layoutName string, err error) bool {
	switch {
	case err == nil:
		return true
	case errors.Is(err, storage.ErrDiskCount):
		l, _ := storage.LayoutByName(layoutName)
		writeError(w, http.StatusBadRequest, "validation.disk_count", err.Error(),
			map[string]any{"min": l.MinDisks, "max": l.MaxDisks})
	case errors.Is(err, storage.ErrDiskTooSmall):
		writeError(w, http.StatusBadRequest, "storage.disk_too_small", err.Error(),
			map[string]any{"minBytes": storage.MinDiskBytes})
	default:
		writeError(w, http.StatusBadRequest, "validation.layout", err.Error(), nil)
	}
	return false
}

func (s *Server) handlePools(w http.ResponseWriter, r *http.Request, _ store.User) {
	pools, err := s.d.Store.Pools(r.Context())
	if err != nil {
		s.internalError(w, "list pools", err)
		return
	}
	out := make([]poolView, 0, len(pools))
	for _, p := range pools {
		out = append(out, s.poolWithStatus(r.Context(), p))
	}
	writeJSON(w, http.StatusOK, map[string]any{"pools": out})
}

func (s *Server) handlePool(w http.ResponseWriter, r *http.Request, _ store.User) {
	p, ok := s.lookupPool(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pool": s.poolWithStatus(r.Context(), p)})
}

func (s *Server) lookupPool(w http.ResponseWriter, r *http.Request) (store.Pool, bool) {
	name := r.PathValue("name")
	if !storage.ValidPoolName(name) {
		writeError(w, http.StatusNotFound, "storage.pool_not_found", "Pool not found", nil)
		return store.Pool{}, false
	}
	p, err := s.d.Store.PoolByName(r.Context(), name)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "storage.pool_not_found", "Pool not found", nil)
		return store.Pool{}, false
	}
	if err != nil {
		s.internalError(w, "load pool", err)
		return store.Pool{}, false
	}
	return p, true
}

func (s *Server) poolWithStatus(ctx context.Context, p store.Pool) poolView {
	v := poolView{Pool: p}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var st storage.PoolStatus
	if err := s.d.Helper.Call(ctx, helper.OpPoolStatus, helper.PoolArgs{Name: p.Name}, &st); err != nil {
		v.StatusError = "helper.unavailable"
		var he *helper.Error
		if errors.As(err, &he) {
			v.StatusError = he.Code
		}
		return v
	}
	st.Layout = p.Layout
	// After a degraded period, "ok" only once a later scrub checked the copies (ADR-0027).
	if err := resync.Apply(ctx, s.d.Store, &st, s.d.Now(), s.bootID()); err != nil {
		s.d.Logger.Warn("resync mark", "pool", p.Name, "err", err)
	}
	v.Status = &st
	return v
}

func (s *Server) handleCreatePool(w http.ResponseWriter, r *http.Request, user store.User) {
	var body struct {
		Name    string   `json:"name"`
		Layout  string   `json:"layout"`
		Disks   []string `json:"disks"`
		Confirm string   `json:"confirm"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	switch {
	case !storage.ValidPoolName(body.Name):
		writeError(w, http.StatusBadRequest, "validation.pool_name", "Invalid pool name", nil)
		return
	case body.Confirm != body.Name:
		writeError(w, http.StatusBadRequest, "validation.confirm", "Type the pool name to confirm", nil)
		return
	}
	if _, ok := storage.LayoutByName(body.Layout); !ok {
		writeError(w, http.StatusBadRequest, "validation.layout", "Unknown layout", nil)
		return
	}
	if _, err := s.d.Store.PoolByName(r.Context(), body.Name); err == nil {
		writeError(w, http.StatusConflict, "storage.pool_exists", "A pool with this name exists", nil)
		return
	}
	sizes, ok := s.availableDiskSizes(w, r, body.Disks)
	if !ok {
		return
	}
	if _, err := storage.PlanPool(body.Layout, sizes); !s.planError(w, body.Layout, err) {
		return
	}

	// Formatting and the md initial sync setup can take minutes on slow USB disks.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(10 * time.Minute))
	// Once started, the creation must finish AND be recorded even if the
	// browser disconnects (tab closed, page reloaded): otherwise the disks are
	// formatted but the pool is unknown. Hence a context that the request's
	// cancellation does not reach.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(helper.WithActor(r.Context(), user.Username)), 8*time.Minute)
	defer cancel()
	var res helper.PoolCreateResult
	args := helper.PoolCreateArgs{Name: body.Name, Layout: body.Layout, Disks: body.Disks, Confirm: body.Confirm}
	err := s.d.Helper.Call(ctx, helper.OpPoolCreate, args, &res)
	s.invalidateDisks()
	if err != nil {
		s.audit(r.WithContext(ctx), user.Username, "pool.create", body.Name, "error", errDetail(err))
		s.respondHelperError(w, "pool.create", err)
		return
	}
	pool, err := s.d.Store.CreatePool(ctx, store.Pool{
		Name: res.Name, Layout: res.Layout, UUID: res.UUID, MDDevice: res.MDDevice, Members: res.Members,
	})
	if err != nil {
		// The pool exists on disk: record the failure loudly, it can be re-imported.
		s.d.Logger.Error("pool created but not recorded", "pool", res.Name, "uuid", res.UUID, "err", err)
		s.internalError(w, "record pool", err)
		return
	}
	s.audit(r.WithContext(ctx), user.Username, "pool.create", pool.Name, "ok", pool.Layout+" "+strings.Join(pool.Members, ","))
	writeJSON(w, http.StatusCreated, map[string]any{"pool": s.poolWithStatus(ctx, pool)})
}

func (s *Server) handleScrub(w http.ResponseWriter, r *http.Request, user store.User) {
	s.scrubAction(w, r, user, helper.OpPoolScrub, "", "started")
}

// handleScrubCancel stops a running scrub; btrfs keeps its progress.
func (s *Server) handleScrubCancel(w http.ResponseWriter, r *http.Request, user store.User) {
	s.scrubAction(w, r, user, helper.OpPoolScrubCancel, "canceled", "canceled")
}

// handleScrubResume goes on with a stopped or interrupted scrub.
func (s *Server) handleScrubResume(w http.ResponseWriter, r *http.Request, user store.User) {
	s.scrubAction(w, r, user, helper.OpPoolScrubResume, "resumed", "resumed")
}

func (s *Server) scrubAction(w http.ResponseWriter, r *http.Request, user store.User, op, detail, key string) {
	p, ok := s.lookupPool(w, r)
	if !ok {
		return
	}
	ctx := helper.WithActor(r.Context(), user.Username)
	if err := s.d.Helper.Call(ctx, op, helper.PoolArgs{Name: p.Name}, nil); err != nil {
		s.helperError(w, r, user, "pool.scrub", err)
		return
	}
	s.audit(r, user.Username, "pool.scrub", p.Name, "ok", detail)
	writeJSON(w, http.StatusAccepted, map[string]bool{key: true})
}

func (s *Server) invalidateDisks() {
	if inv, ok := s.d.Disks.(interface{ Invalidate() }); ok {
		inv.Invalidate()
	}
}

func errDetail(err error) string {
	var he *helper.Error
	if errors.As(err, &he) {
		return he.Code + ": " + he.Message
	}
	return err.Error()
}
