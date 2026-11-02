// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
)

// App is an application's desired state: its compose file (ADR-0013).
type App struct {
	Name      string `json:"name"`
	Pool      string `json:"pool"`
	Template  string `json:"template"` // catalog entry, "" for a custom file
	Compose   string `json:"compose"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

// SaveApp creates or updates an application. The pool of an existing
// application never changes (its data lives there).
func (s *Store) SaveApp(ctx context.Context, a App) (App, error) {
	now := s.now().Unix()
	_, err := s.db.ExecContext(ctx, `INSERT INTO apps (name, pool, template, compose, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET template = excluded.template, compose = excluded.compose, updated_at = excluded.updated_at`,
		a.Name, a.Pool, a.Template, a.Compose, now, now)
	if err != nil {
		return App{}, err
	}
	return s.AppByName(ctx, a.Name)
}

// AppByName returns one application or ErrNotFound.
func (s *Store) AppByName(ctx context.Context, name string) (App, error) {
	var a App
	err := s.db.QueryRowContext(ctx, "SELECT name, pool, template, compose, created_at, updated_at FROM apps WHERE name = ?", name).
		Scan(&a.Name, &a.Pool, &a.Template, &a.Compose, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return App{}, ErrNotFound
	}
	return a, err
}

// Apps lists applications by name.
func (s *Store) Apps(ctx context.Context) ([]App, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT name, pool, template, compose, created_at, updated_at FROM apps ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []App{}
	for rows.Next() {
		var a App
		if err := rows.Scan(&a.Name, &a.Pool, &a.Template, &a.Compose, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// DeleteApp removes an application record. ErrNotFound if unknown.
func (s *Store) DeleteApp(ctx context.Context, name string) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM apps WHERE name = ?", name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
