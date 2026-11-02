// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/crosswax/nasbuster/internal/alerts"
	"github.com/crosswax/nasbuster/internal/scheduler"
	"github.com/crosswax/nasbuster/internal/store"
)

var alertIDRe = regexp.MustCompile(`^[a-z_.]{1,64}(:[0-9A-Za-z#+.:=@_-]{1,255})?$`)

func (s *Server) handleAlerts(w http.ResponseWriter, r *http.Request, _ store.User) {
	days := 7
	if v := r.URL.Query().Get("resolvedDays"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > 365 {
			writeError(w, http.StatusBadRequest, "validation.days", "resolvedDays must be between 0 and 365", nil)
			return
		}
		days = n
	}
	since := time.Now().Add(-time.Duration(days) * 24 * time.Hour).Unix()
	list, err := s.d.Store.Alerts(r.Context(), since)
	if err != nil {
		s.internalError(w, "list alerts", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"alerts": list})
}

func (s *Server) handleAckAlert(w http.ResponseWriter, r *http.Request, user store.User) {
	id := r.PathValue("id")
	if !alertIDRe.MatchString(id) {
		writeError(w, http.StatusNotFound, "alerts.not_found", "Alert not found", nil)
		return
	}
	err := s.d.Store.AckAlert(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "alerts.not_found", "Alert not found", nil)
		return
	}
	if err != nil {
		s.internalError(w, "ack alert", err)
		return
	}
	s.audit(r, user.Username, "alert.ack", id, "ok", "")
	w.WriteHeader(http.StatusNoContent)
}

// notificationsView never contains secrets.
type notificationsView struct {
	Ntfy struct {
		Enabled  bool   `json:"enabled"`
		URL      string `json:"url"`
		TokenSet bool   `json:"tokenSet"`
	} `json:"ntfy"`
	Webhook alerts.WebhookConfig `json:"webhook"`
}

func viewChannels(ch alerts.Channels) notificationsView {
	var v notificationsView
	v.Ntfy.Enabled, v.Ntfy.URL, v.Ntfy.TokenSet = ch.Ntfy.Enabled, ch.Ntfy.URL, ch.Ntfy.Token != ""
	v.Webhook = ch.Webhook
	return v
}

func (s *Server) handleGetNotifications(w http.ResponseWriter, r *http.Request, _ store.User) {
	ch, err := scheduler.LoadChannels(r.Context(), s.d.Store)
	if err != nil {
		s.internalError(w, "load notifications", err)
		return
	}
	writeJSON(w, http.StatusOK, viewChannels(ch))
}

func (s *Server) handlePutNotifications(w http.ResponseWriter, r *http.Request, user store.User) {
	var body struct {
		Ntfy struct {
			Enabled bool    `json:"enabled"`
			URL     string  `json:"url"`
			Token   *string `json:"token"` // absent: keep; "": clear; value: set
		} `json:"ntfy"`
		Webhook alerts.WebhookConfig `json:"webhook"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	for _, c := range []struct {
		channel string
		on      bool
		url     string
	}{{"ntfy", body.Ntfy.Enabled, body.Ntfy.URL}, {"webhook", body.Webhook.Enabled, body.Webhook.URL}} {
		if c.on || c.url != "" {
			if err := alerts.ValidURL(c.url); err != nil {
				writeError(w, http.StatusBadRequest, "validation.url", err.Error(), map[string]any{"channel": c.channel})
				return
			}
		}
	}
	current, err := scheduler.LoadChannels(r.Context(), s.d.Store)
	if err != nil {
		s.internalError(w, "load notifications", err)
		return
	}
	next := alerts.Channels{
		Ntfy:    alerts.NtfyConfig{Enabled: body.Ntfy.Enabled, URL: body.Ntfy.URL, Token: current.Ntfy.Token},
		Webhook: body.Webhook,
	}
	if body.Ntfy.Token != nil {
		if len(*body.Ntfy.Token) > 256 {
			writeError(w, http.StatusBadRequest, "validation.token", "Token too long", nil)
			return
		}
		next.Ntfy.Token = *body.Ntfy.Token
	}
	if err := s.d.Store.SetSetting(r.Context(), scheduler.NotificationsKey, next); err != nil {
		s.internalError(w, "save notifications", err)
		return
	}
	s.audit(r, user.Username, "notifications.update", "", "ok", "")
	writeJSON(w, http.StatusOK, viewChannels(next))
}

func (s *Server) handleTestNotifications(w http.ResponseWriter, r *http.Request, user store.User) {
	ch, err := scheduler.LoadChannels(r.Context(), s.d.Store)
	if err != nil {
		s.internalError(w, "load notifications", err)
		return
	}
	lang := s.d.Store.AdminLanguage(r.Context())
	nas := s.d.Prober.Info().Hostname
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	// Demo mode never talks to the Internet (a default ntfy topic is public):
	// only local-network targets receive a real test.
	var results []alerts.Result
	if s.d.Demo && ch.Ntfy.Enabled && !alerts.IsLocalURL(ch.Ntfy.URL) {
		ch.Ntfy.Enabled = false
		results = append(results, alerts.Result{Channel: "ntfy", OK: true, Code: "notify.demo"})
	}
	if s.d.Demo && ch.Webhook.Enabled && !alerts.IsLocalURL(ch.Webhook.URL) {
		ch.Webhook.Enabled = false
		results = append(results, alerts.Result{Channel: "webhook", OK: true, Code: "notify.demo"})
	}
	results = append(results, alerts.NewNotifier().Send(ctx, ch, alerts.Message{NAS: nas, Title: alerts.Title("test", lang, nil),
		Level: "info", Code: "test", Params: map[string]any{}, Time: time.Now().Unix()})...)
	if results == nil {
		results = []alerts.Result{}
	}
	s.audit(r, user.Username, "notifications.test", "", "ok", "")
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}
