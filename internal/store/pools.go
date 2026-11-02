// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// Pool is the desired state of a storage pool. Its live state (mount, health,
// scrub) is always read from the system, never stored.
type Pool struct {
	Name      string   `json:"name"`
	Layout    string   `json:"layout"`
	UUID      string   `json:"uuid"`
	MDDevice  string   `json:"mdDevice"`
	Members   []string `json:"members"`
	CreatedAt int64    `json:"createdAt"`
}

// CreatePool records a pool. ErrExists if the name is taken.
func (s *Store) CreatePool(ctx context.Context, p Pool) (Pool, error) {
	members, err := json.Marshal(p.Members)
	if err != nil {
		return Pool{}, err
	}
	p.CreatedAt = s.now().Unix()
	_, err = s.db.ExecContext(ctx,
		"INSERT INTO pools (name, layout, uuid, md_device, members, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		p.Name, p.Layout, p.UUID, p.MDDevice, string(members), p.CreatedAt)
	if isUniqueViolation(err) {
		return Pool{}, ErrExists
	}
	return p, err
}

// Pools lists pools by name.
func (s *Store) Pools(ctx context.Context) ([]Pool, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT name, layout, uuid, md_device, members, created_at FROM pools ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Pool{}
	for rows.Next() {
		p, err := scanPool(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PoolByName returns one pool or ErrNotFound.
func (s *Store) PoolByName(ctx context.Context, name string) (Pool, error) {
	p, err := scanPool(s.db.QueryRowContext(ctx, "SELECT name, layout, uuid, md_device, members, created_at FROM pools WHERE name = ?", name))
	if errors.Is(err, sql.ErrNoRows) {
		return Pool{}, ErrNotFound
	}
	return p, err
}

// SetPoolMembers records the disks of a pool after a replacement.
func (s *Store) SetPoolMembers(ctx context.Context, name string, members []string) error {
	b, err := json.Marshal(members)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, "UPDATE pools SET members = ? WHERE name = ?", string(b), name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func scanPool(row interface{ Scan(...any) error }) (Pool, error) {
	var p Pool
	var members string
	if err := row.Scan(&p.Name, &p.Layout, &p.UUID, &p.MDDevice, &members, &p.CreatedAt); err != nil {
		return Pool{}, err
	}
	if err := json.Unmarshal([]byte(members), &p.Members); err != nil {
		return Pool{}, ErrInvalidData
	}
	return p, nil
}

// DeletePool removes a pool record. Its shares must be gone (foreign key).
func (s *Store) DeletePool(ctx context.Context, name string) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM pools WHERE name = ?", name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
