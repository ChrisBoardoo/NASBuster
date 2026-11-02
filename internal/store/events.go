// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"encoding/json"
	"strings"
)

// Event time states (ADR-0031).
const (
	TimeExact     = "exact"     // the clock was synchronized
	TimeUncertain = "uncertain" // not synchronized yet: no RTC, no network
	TimeCorrected = "corrected" // recomputed from the time since boot once synchronized
)

// EventLevels, from the least to the most severe.
var EventLevels = []string{"info", "warn", "error", "crit"}

// Event is one entry of the event journal (API: GET /events).
type Event struct {
	ID        int64          `json:"id"`
	Time      int64          `json:"time"` // unix seconds
	TimeState string         `json:"timeState"`
	BootID    string         `json:"bootId"`
	UptimeMs  int64          `json:"-"` // time since boot when it was recorded
	Level     string         `json:"level"`
	Code      string         `json:"code"`    // lexicon key: events.<code>
	Phase     string         `json:"phase"`   // "raised", "resolved", or "" for a one-off event
	Subject   string         `json:"subject"` // "pool:tank", "disk:<by-id>", "system"
	Source    string         `json:"source"`  // "alert", "system", "kernel"
	Params    map[string]any `json:"params"`
}

// EventFilter narrows a listing.
type EventFilter struct {
	Before   int64  // only entries with a smaller id (paging); 0: the newest
	Limit    int    // 1..500
	MinLevel string // "" or one of EventLevels
	Subject  string // exact subject, or a kind ending with ':' ("pool:")
}

// AddEvent appends an event and returns its id.
func (s *Store) AddEvent(ctx context.Context, e Event) (int64, error) {
	if e.Params == nil {
		e.Params = map[string]any{}
	}
	raw, err := json.Marshal(e.Params)
	if err != nil {
		return 0, err
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO events (ts, ts_state, boot_id, uptime_ms, level, code, phase, subject, source, params)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.Time, e.TimeState, e.BootID, e.UptimeMs, e.Level, e.Code, e.Phase, e.Subject, e.Source, string(raw))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Events lists events, newest first.
func (s *Store) Events(ctx context.Context, f EventFilter) ([]Event, error) {
	q := "SELECT id, ts, ts_state, boot_id, uptime_ms, level, code, phase, subject, source, params FROM events WHERE 1 = 1"
	var args []any
	if f.Before > 0 {
		q += " AND id < ?"
		args = append(args, f.Before)
	}
	if f.MinLevel != "" {
		var levels []string
		for i, l := range EventLevels {
			if l == f.MinLevel {
				levels = EventLevels[i:]
			}
		}
		if levels == nil {
			return nil, ErrInvalidData
		}
		q += " AND level IN (?" + strings.Repeat(", ?", len(levels)-1) + ")"
		for _, l := range levels {
			args = append(args, l)
		}
	}
	switch {
	case strings.HasSuffix(f.Subject, ":"):
		q += " AND substr(subject, 1, ?) = ?"
		args = append(args, len(f.Subject), f.Subject)
	case f.Subject != "":
		q += " AND subject = ?"
		args = append(args, f.Subject)
	}
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q += " ORDER BY id DESC LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		var params string
		if err := rows.Scan(&e.ID, &e.Time, &e.TimeState, &e.BootID, &e.UptimeMs, &e.Level, &e.Code, &e.Phase, &e.Subject, &e.Source, &params); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(params), &e.Params); err != nil || e.Params == nil {
			e.Params = map[string]any{}
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// HasUncertainEvents reports whether events of this boot still wait for a
// synchronized clock (a read: nothing is written when there is nothing to fix).
func (s *Store) HasUncertainEvents(ctx context.Context, bootID string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM events WHERE boot_id = ? AND ts_state = ?)", bootID, TimeUncertain).Scan(&n)
	return n == 1, err
}

// CorrectEventTimes recomputes the uncertain times of a boot from the real
// time of that boot (unix milliseconds) and the time since boot of each event.
func (s *Store) CorrectEventTimes(ctx context.Context, bootID string, bootTimeMs int64) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE events SET ts = (? + uptime_ms) / 1000, ts_state = ?
		WHERE boot_id = ? AND ts_state = ?`, bootTimeMs, TimeCorrected, bootID, TimeUncertain)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// PruneEvents keeps the newest `keep` events, none older than `before` (unix).
func (s *Store) PruneEvents(ctx context.Context, keep int, before int64) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM events WHERE ts < ? OR id <= (SELECT COALESCE(MAX(id), 0) FROM events) - ?`, before, keep)
	return err
}
