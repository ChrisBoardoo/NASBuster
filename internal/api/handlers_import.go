// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/crosswax/nasbuster/internal/helper"
	"github.com/crosswax/nasbuster/internal/shares"
	"github.com/crosswax/nasbuster/internal/store"
)

// Copying data from a foreign disk (OMV, USB) into a share (ADR-0015).

func (s *Server) handleImportSources(w http.ResponseWriter, r *http.Request, _ store.User) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	var out helper.ImportSources
	if err := s.d.Helper.Call(ctx, helper.OpImportSources, nil, &out); err != nil {
		s.respondHelperError(w, "import.sources", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleImportMount(w http.ResponseWriter, r *http.Request, user store.User) {
	var body helper.ImportMountArgs
	if !decodeJSON(w, r, &body) {
		return
	}
	ctx, cancel := repairCtx(r, user)
	defer cancel()
	var out helper.ImportMounted
	if err := s.d.Helper.Call(ctx, helper.OpImportMount, body, &out); err != nil {
		s.helperError(w, r, user, "import.mount", err)
		return
	}
	s.audit(r, user.Username, "import.mount", body.UUID, "ok", "read-only")
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleImportUnmount(w http.ResponseWriter, r *http.Request, user store.User) {
	var body helper.ImportMountArgs
	if !decodeJSON(w, r, &body) {
		return
	}
	ctx, cancel := repairCtx(r, user)
	defer cancel()
	if err := s.d.Helper.Call(ctx, helper.OpImportUnmount, body, nil); err != nil {
		s.helperError(w, r, user, "import.unmount", err)
		return
	}
	s.audit(r, user.Username, "import.unmount", body.UUID, "ok", "")
	writeJSON(w, http.StatusOK, map[string]bool{"unmounted": true})
}

func (s *Server) handleImportCopy(w http.ResponseWriter, r *http.Request, user store.User) {
	var body struct {
		UUID   string `json:"uuid"`
		Share  string `json:"share"`
		Subdir string `json:"subdir"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	sh, err := s.d.Store.ShareByName(r.Context(), body.Share)
	if errors.Is(err, store.ErrNotFound) || !shares.ValidShareName(body.Share) {
		writeError(w, http.StatusNotFound, "shares.not_found", "Share not found", nil)
		return
	}
	if err != nil {
		s.internalError(w, "load share", err)
		return
	}
	ctx, cancel := repairCtx(r, user)
	defer cancel()
	var out map[string]string
	args := helper.ImportCopyArgs{UUID: body.UUID, Pool: sh.Pool, Share: sh.Name, Subdir: body.Subdir}
	if err := s.d.Helper.Call(ctx, helper.OpImportCopy, args, &out); err != nil {
		s.helperError(w, r, user, "import.copy", err)
		return
	}
	s.audit(r, user.Username, "import.copy", sh.Name, "ok", out["dest"])
	writeJSON(w, http.StatusAccepted, out)
}

func (s *Server) handleImportProgress(w http.ResponseWriter, r *http.Request, _ store.User) {
	var out helper.ImportProgress
	if err := s.d.Helper.Call(r.Context(), helper.OpImportProgress, helper.ImportShareArgs{Share: r.PathValue("share")}, &out); err != nil {
		s.respondHelperError(w, "import.progress", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
