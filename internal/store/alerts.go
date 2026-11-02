// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// Alert is a stored alert (API: GET /alerts).
type Alert struct {
	ID         string         `json:"id"`
	Level      string         `json:"level"`
	Code       string         `json:"code"`
	Params     map[string]any `json:"params"`
	FirstSeen  int64          `json:"firstSeen"`
	LastSeen   int64          `json:"lastSeen"`
	ResolvedAt int64          `json:"resolvedAt"` // 0 while active
	AckedAt    int64          `json:"ackedAt"`    // 0 until acknowledged
}

// ActiveAlerts returns id -> level of the alerts that still hold.
func (s *Store) ActiveAlerts(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, level FROM alerts WHERE resolved_at = 0")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, level string
		if err := rows.Scan(&id, &level); err != nil {
			return nil, err
		}
		out[id] = level
	}
	return out, rows.Err()
}

// RaiseAlert records a new (or worsened) alert. A previously resolved alert
// with the same id starts over (new first_seen, not acknowledged).
func (s *Store) RaiseAlert(ctx context.Context, id, level, code string, params map[string]any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	now := s.now().Unix()
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO alerts (id, level, code, params, first_seen, last_seen) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			level = excluded.level, code = excluded.code, params = excluded.params, last_seen = excluded.last_seen,
			first_seen = CASE WHEN alerts.resolved_at = 0 THEN alerts.first_seen ELSE excluded.first_seen END,
			acked_at   = CASE WHEN alerts.resolved_at = 0 THEN alerts.acked_at ELSE 0 END,
			resolved_at = 0`,
		id, level, code, string(raw), now, now)
	return err
}

// TouchAlert updates last_seen of an alert that still holds.
func (s *Store) TouchAlert(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE alerts SET last_seen = ? WHERE id = ? AND resolved_at = 0", s.now().Unix(), id)
	return err
}

// ResolveAlert marks an alert as no longer holding. It returns the alert as
// it was (for the resolution notification).
func (s *Store) ResolveAlert(ctx context.Context, id string) (Alert, error) {
	a, err := s.alert(ctx, id)
	if err != nil {
		return Alert{}, err
	}
	a.ResolvedAt = s.now().Unix()
	_, err = s.db.ExecContext(ctx, "UPDATE alerts SET resolved_at = ? WHERE id = ?", a.ResolvedAt, id)
	return a, err
}

// AckAlert records that the administrator saw the alert.
func (s *Store) AckAlert(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, "UPDATE alerts SET acked_at = ? WHERE id = ? AND acked_at = 0", s.now().Unix(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if _, err := s.alert(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// Alerts lists active alerts first (most severe, then newest), then alerts
// resolved after `resolvedSince` (unix).
func (s *Store) Alerts(ctx context.Context, resolvedSince int64) ([]Alert, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, level, code, params, first_seen, last_seen, resolved_at, acked_at FROM alerts
		WHERE resolved_at = 0 OR resolved_at >= ?
		ORDER BY resolved_at = 0 DESC, level = 'crit' DESC, first_seen DESC`, resolvedSince)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Alert{}
	for rows.Next() {
		a, err := scanAlert(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) alert(ctx context.Context, id string) (Alert, error) {
	a, err := scanAlert(s.db.QueryRowContext(ctx,
		"SELECT id, level, code, params, first_seen, last_seen, resolved_at, acked_at FROM alerts WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return Alert{}, ErrNotFound
	}
	return a, err
}

func scanAlert(row interface{ Scan(...any) error }) (Alert, error) {
	var a Alert
	var params string
	if err := row.Scan(&a.ID, &a.Level, &a.Code, &params, &a.FirstSeen, &a.LastSeen, &a.ResolvedAt, &a.AckedAt); err != nil {
		return Alert{}, err
	}
	if err := json.Unmarshal([]byte(params), &a.Params); err != nil || a.Params == nil {
		a.Params = map[string]any{}
	}
	return a, nil
}

// AdminLanguage is the UI language of the first administrator (used for
// notification texts), "en" by default.
func (s *Store) AdminLanguage(ctx context.Context) string {
	var lang string
	if err := s.db.QueryRowContext(ctx, "SELECT language FROM users ORDER BY id LIMIT 1").Scan(&lang); err != nil || lang == "" {
		return "en"
	}
	return lang
}
