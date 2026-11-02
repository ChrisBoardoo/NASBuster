// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strconv"

	"github.com/crosswax/nasbuster/internal/events"
	"github.com/crosswax/nasbuster/internal/helper"
	"github.com/crosswax/nasbuster/internal/store"
)

// journalSubjectRe: a subject ("pool:tank", "disk:<by-id>", "system"), or a
// kind alone ("pool:") to list every subject of that kind.
var journalSubjectRe = regexp.MustCompile(`^(system|pool:[a-z0-9_-]{0,32}|disk:[0-9A-Za-z#+.:=@_-]{0,255})$`)

// handleJournal lists the event journal, newest first (ADR-0031). Not
// /events: that is the metrics stream.
func (s *Server) handleJournal(w http.ResponseWriter, r *http.Request, _ store.User) {
	q := r.URL.Query()
	f := store.EventFilter{Limit: 100}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 500 {
			writeError(w, http.StatusBadRequest, "validation.limit", "limit must be between 1 and 500", nil)
			return
		}
		f.Limit = n
	}
	if v := q.Get("before"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "validation.before", "before must be an event id", nil)
			return
		}
		f.Before = n
	}
	if v := q.Get("level"); v != "" {
		if !slices.Contains(store.EventLevels, v) {
			writeError(w, http.StatusBadRequest, "validation.level", "level must be info, warn, error or crit", nil)
			return
		}
		f.MinLevel = v
	}
	if v := q.Get("subject"); v != "" {
		if !journalSubjectRe.MatchString(v) {
			writeError(w, http.StatusBadRequest, "validation.subject", "invalid subject", nil)
			return
		}
		f.Subject = v
	}
	list, err := s.d.Store.Events(r.Context(), f)
	if errors.Is(err, store.ErrInvalidData) {
		writeError(w, http.StatusBadRequest, "validation.level", "invalid level", nil)
		return
	}
	if err != nil {
		s.internalError(w, "list journal", err)
		return
	}
	// next: the id to pass as "before" for the following page, 0 at the end.
	var next int64
	if len(list) == f.Limit {
		next = list[len(list)-1].ID
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": list, "next": next})
}

// journalSettingsView: the system journal kept across reboots (ADR-0031).
type journalSettingsView struct {
	Persistent bool   `json:"persistent"` // the administrator's choice, on by default
	Active     bool   `json:"active"`     // NasBuster's drop-in is in place
	ManagedBy  string `json:"managedBy"`  // another file sets Storage=: left alone
}

func (s *Server) handleGetJournalSettings(w http.ResponseWriter, r *http.Request, user store.User) {
	set, err := events.LoadSettings(r.Context(), s.d.Store)
	if err != nil {
		s.internalError(w, "load journal settings", err)
		return
	}
	var res helper.JournalPersistResult
	if err := s.d.Helper.Call(r.Context(), helper.OpJournalPersist, helper.JournalPersistArgs{Status: true}, &res); err != nil {
		s.helperError(w, r, user, "journal.settings", err)
		return
	}
	writeJSON(w, http.StatusOK, journalSettingsView{Persistent: set.Persistent, Active: res.Persistent, ManagedBy: res.ManagedBy})
}

func (s *Server) handlePutJournalSettings(w http.ResponseWriter, r *http.Request, user store.User) {
	var body events.Settings
	if !decodeJSON(w, r, &body) {
		return
	}
	if err := s.d.Store.SetSetting(r.Context(), events.SettingsKey, body); err != nil {
		s.internalError(w, "save journal settings", err)
		return
	}
	var res helper.JournalPersistResult
	ctx := helper.WithActor(r.Context(), user.Username)
	if err := s.d.Helper.Call(ctx, helper.OpJournalPersist, helper.JournalPersistArgs{Enabled: body.Persistent}, &res); err != nil {
		s.helperError(w, r, user, "journal.persistent", err)
		return
	}
	detail := "off"
	if body.Persistent {
		detail = "on"
	}
	s.audit(r, user.Username, "journal.persistent", "", "ok", detail)
	writeJSON(w, http.StatusOK, journalSettingsView{Persistent: body.Persistent, Active: res.Persistent, ManagedBy: res.ManagedBy})
}
