// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/crosswax/nasbuster/internal/helper"
	"github.com/crosswax/nasbuster/internal/storage"
	"github.com/crosswax/nasbuster/internal/store"
	"github.com/crosswax/nasbuster/internal/sysexec"
	"github.com/crosswax/nasbuster/internal/validate"
)

// onboardingSteps in order (api-v1.md).
var onboardingSteps = []string{
	"welcome", "health", "account", "identity", "disks", "protection",
	"confirm", "create", "share", "apps", "safety", "done",
}

type onboardingState struct {
	Completed      bool     `json:"completed"`
	CurrentStep    string   `json:"currentStep"`
	CompletedSteps []string `json:"completedSteps"`
}

func (s *Server) handleGetOnboarding(w http.ResponseWriter, r *http.Request, _ store.User) {
	state := onboardingState{CurrentStep: "welcome", CompletedSteps: []string{}}
	if err := s.d.Store.GetSetting(r.Context(), "onboarding", &state); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.internalError(w, "read onboarding", err)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) handlePutOnboarding(w http.ResponseWriter, r *http.Request, user store.User) {
	var state onboardingState
	if !decodeJSON(w, r, &state) {
		return
	}
	if state.CompletedSteps == nil {
		state.CompletedSteps = []string{}
	}
	valid := slices.Contains(onboardingSteps, state.CurrentStep)
	for _, step := range state.CompletedSteps {
		valid = valid && slices.Contains(onboardingSteps, step)
	}
	if !valid || len(state.CompletedSteps) > len(onboardingSteps) {
		writeError(w, http.StatusBadRequest, "validation.step", "Unknown onboarding step", nil)
		return
	}
	if err := s.d.Store.SetSetting(r.Context(), "onboarding", state); err != nil {
		s.internalError(w, "save onboarding", err)
		return
	}
	if state.Completed {
		s.audit(r, user.Username, "onboarding.completed", "", "ok", "")
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) handleSystemInfo(w http.ResponseWriter, r *http.Request, _ store.User) {
	writeJSON(w, http.StatusOK, s.d.Prober.Info())
}

func (s *Server) handleSystemChecks(w http.ResponseWriter, r *http.Request, _ store.User) {
	writeJSON(w, http.StatusOK, map[string]any{"checks": s.d.Prober.Checks(r.Context())})
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request, _ store.User) {
	cur, ok := s.d.Metrics.Current()
	var current any
	if ok {
		current = cur
	}
	writeJSON(w, http.StatusOK, map[string]any{"current": current, "history": s.d.Metrics.History()})
}

func (s *Server) handleTimezones(w http.ResponseWriter, r *http.Request, _ store.User) {
	writeJSON(w, http.StatusOK, map[string]any{"timezones": s.d.Prober.Timezones()})
}

func (s *Server) handleIdentity(w http.ResponseWriter, r *http.Request, user store.User) {
	var body struct {
		Hostname string `json:"hostname"`
		Timezone string `json:"timezone"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if !validate.Hostname(body.Hostname) {
		writeError(w, http.StatusBadRequest, "validation.hostname", "Invalid hostname", nil)
		return
	}
	if !s.d.Prober.TimezoneExists(body.Timezone) {
		writeError(w, http.StatusBadRequest, "validation.timezone", "Unknown timezone", nil)
		return
	}
	ctx := helper.WithActor(r.Context(), user.Username)
	if err := s.d.Helper.Call(ctx, helper.OpSetHostname, helper.SetHostnameArgs{Hostname: body.Hostname}, nil); err != nil {
		s.helperError(w, r, user, "system.identity", err)
		return
	}
	if err := s.d.Helper.Call(ctx, helper.OpSetTimezone, helper.SetTimezoneArgs{Timezone: body.Timezone}, nil); err != nil {
		s.helperError(w, r, user, "system.identity", err)
		return
	}
	s.audit(r, user.Username, "system.identity", body.Hostname, "ok", body.Timezone)
	writeJSON(w, http.StatusOK, body)
}

// helperStatus maps helper error codes that are user-facing outcomes (not
// failures of the helper itself) to HTTP statuses.
var helperStatus = map[string]int{
	helper.CodeDiskNotFound:     http.StatusNotFound,
	helper.CodeDiskNotAvailable: http.StatusConflict,
	helper.CodeDiskResidual:     http.StatusConflict,
	helper.CodePoolExists:       http.StatusConflict,
	helper.CodePoolNotMounted:   http.StatusConflict,
	helper.CodePoolNotFound:     http.StatusNotFound,
	helper.CodePoolMounted:      http.StatusConflict,
	helper.CodePoolBusy:         http.StatusConflict,
	helper.CodeNothingMissing:   http.StatusConflict,
	helper.CodeDiskMissing:      http.StatusConflict,
	helper.CodeMemberNotFound:   http.StatusConflict,
	helper.CodeMemberHealthy:    http.StatusConflict,
	helper.CodeScrubRequired:    http.StatusConflict,
	helper.CodeDiskProtected:    http.StatusForbidden,
	helper.CodeDiskMounted:      http.StatusConflict,
	helper.CodeDiskBusy:         http.StatusConflict,
	helper.CodeDiskInPool:       http.StatusConflict,
	helper.CodePoolNotEmpty:     http.StatusConflict,
	helper.CodePoolInUse:        http.StatusConflict,
	helper.CodePoolIncomplete:   http.StatusConflict,
	helper.CodeNothingToResume:  http.StatusConflict,
	helper.CodePoolBroken:       http.StatusConflict,
	helper.CodeCannotReassemble: http.StatusConflict,
	helper.CodeScrubNotRunning:  http.StatusConflict,
	helper.CodeScrubNotStopped:  http.StatusConflict,
	helper.CodeBackupInvalid:    http.StatusUnprocessableEntity,
	helper.CodeImportNotMounted: http.StatusConflict,
	helper.CodeComposeInvalid:   http.StatusBadRequest,
	helper.CodeEngineMissing:    http.StatusConflict,
	helper.CodeEngineInstalled:  http.StatusConflict,
	helper.CodeJobRunning:       http.StatusConflict,
	helper.CodeAppNotFound:      http.StatusNotFound,
	helper.CodeEngineInUse:      http.StatusConflict,
	helper.CodeDiskTooSmall:     http.StatusBadRequest,
	helper.CodeUserExists:       http.StatusConflict,
	helper.CodeUserNameTaken:    http.StatusConflict,
	helper.CodeUserNotFound:     http.StatusNotFound,
	helper.CodeShareExists:      http.StatusConflict,
	helper.CodeShareMissing:     http.StatusConflict,
	helper.CodeSnapshotExist:    http.StatusConflict,
	helper.CodeSnapshotNone:     http.StatusNotFound,
}

// helperError maps helper failures to API errors and records real failures
// (not refusals) in the audit log.
func (s *Server) helperError(w http.ResponseWriter, r *http.Request, user store.User, action string, err error) {
	if failed := s.respondHelperError(w, action, err); failed {
		s.audit(r, user.Username, action, "", "error", err.Error())
	}
}

// respondHelperError writes the API error for a helper failure. It reports
// whether the helper itself failed (as opposed to refusing invalid input).
func (s *Server) respondHelperError(w http.ResponseWriter, action string, err error) bool {
	var he *helper.Error
	switch {
	case errors.Is(err, helper.ErrUnavailable):
		s.d.Logger.Warn("helper unavailable", "action", action, "err", err)
		writeError(w, http.StatusServiceUnavailable, "helper.unavailable", "Privileged helper unavailable", nil)
		return false
	case errors.As(err, &he) && strings.HasPrefix(he.Code, "validation."):
		writeError(w, http.StatusBadRequest, he.Code, he.Message, he.Params)
		return false
	case errors.As(err, &he) && helperStatus[he.Code] != 0:
		writeError(w, helperStatus[he.Code], he.Code, he.Message, he.Params)
		return false
	default:
		s.d.Logger.Error("helper failed", "action", action, "err", err)
		// The technical cause helps the administrator (the only kind of user).
		msg := "Privileged operation failed"
		if errors.As(err, &he) && he.Message != "" {
			msg = he.Message
		}
		writeError(w, http.StatusBadGateway, "helper.failed", msg, nil)
		return true
	}
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request, _ store.User) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 500 {
			writeError(w, http.StatusBadRequest, "validation.limit", "limit must be between 1 and 500", nil)
			return
		}
		limit = n
	}
	entries, err := s.d.Store.AuditList(r.Context(), limit)
	if err != nil {
		s.internalError(w, "list audit", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

func (s *Server) handleDisks(w http.ResponseWriter, r *http.Request, _ store.User) {
	disks, err := s.d.Disks.Disks(r.Context())
	if err != nil {
		s.storageError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"disks": disks})
}

func (s *Server) handleSmart(w http.ResponseWriter, r *http.Request, user store.User) {
	id := r.PathValue("id")
	if !validate.DiskID(id) {
		writeError(w, http.StatusNotFound, "storage.disk_not_found", "Disk not found", nil)
		return
	}
	if _, err := s.d.Disks.Disk(r.Context(), id); err != nil {
		s.storageError(w, err)
		return
	}
	var report storage.SmartReport
	ctx := helper.WithActor(r.Context(), user.Username)
	if err := s.d.Helper.Call(ctx, helper.OpDiskSmart, helper.DiskSmartArgs{ID: id}, &report); err != nil {
		s.helperError(w, r, user, "disk.smart", err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) storageError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, storage.ErrNotFound):
		writeError(w, http.StatusNotFound, "storage.disk_not_found", "Disk not found", nil)
	case errors.Is(err, sysexec.ErrNotFound):
		writeError(w, http.StatusServiceUnavailable, "storage.unavailable", "Disk tools are not installed", nil)
	default:
		s.internalError(w, "list disks", err)
	}
}

// handleEvents streams metrics as Server-Sent Events.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request, _ store.User) {
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{}) // long-lived
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	samples, cancel := s.d.Metrics.Subscribe()
	defer cancel()
	send := func(v any) bool {
		b, _ := json.Marshal(v)
		if _, err := w.Write([]byte("event: metrics\ndata: " + string(b) + "\n\n")); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	if cur, ok := s.d.Metrics.Current(); ok && !send(cur) {
		return
	}
	keepAlive := time.NewTicker(15 * time.Second)
	defer keepAlive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case sample := <-samples:
			if !send(sample) {
				return
			}
		case <-keepAlive.C:
			if _, err := w.Write([]byte(": keep-alive\n\n")); err != nil || rc.Flush() != nil {
				return
			}
		}
	}
}
