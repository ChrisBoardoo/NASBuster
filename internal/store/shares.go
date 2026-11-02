// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"strings"
)

// NASUser is a person who accesses shares. Passwords live in Samba only.
type NASUser struct {
	Name      string        `json:"name"`
	CreatedAt int64         `json:"createdAt"`
	Shares    []ShareAccess `json:"shares"` // shares this user can reach
}

// ShareAccess is one grant: from a share's view User is set, from a user's
// view Share is set.
type ShareAccess struct {
	Share string `json:"share,omitempty"`
	User  string `json:"user,omitempty"`
	Mode  string `json:"mode"`
}

// Share is the desired state of an SMB share.
type Share struct {
	Name              string        `json:"name"`
	Pool              string        `json:"pool"`
	CreatedAt         int64         `json:"createdAt"`
	Access            []ShareAccess `json:"access"`
	TimeMachine       bool          `json:"timeMachine"`
	TimeMachineMaxGiB int           `json:"timeMachineMaxGiB"`
	Recycle           bool          `json:"recycle"`
	RecycleDays       int           `json:"recycleDays"`
}

// CreateNASUser records a NAS user.
func (s *Store) CreateNASUser(ctx context.Context, name string) (NASUser, error) {
	u := NASUser{Name: name, CreatedAt: s.now().Unix(), Shares: []ShareAccess{}}
	_, err := s.db.ExecContext(ctx, "INSERT INTO nas_users (name, created_at) VALUES (?, ?)", u.Name, u.CreatedAt)
	if isUniqueViolation(err) {
		return NASUser{}, ErrExists
	}
	return u, err
}

// DeleteNASUser removes a user and their grants.
func (s *Store) DeleteNASUser(ctx context.Context, name string) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM nas_users WHERE name = ?", name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// NASUserExists reports whether a NAS user is recorded.
func (s *Store) NASUserExists(ctx context.Context, name string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM nas_users WHERE name = ?", name).Scan(&n)
	return n > 0, err
}

// NASUsers lists users with their grants.
func (s *Store) NASUsers(ctx context.Context) ([]NASUser, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT name, created_at FROM nas_users ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NASUser{}
	index := map[string]int{}
	for rows.Next() {
		u := NASUser{Shares: []ShareAccess{}}
		if err := rows.Scan(&u.Name, &u.CreatedAt); err != nil {
			return nil, err
		}
		index[u.Name] = len(out)
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	grants, err := s.db.QueryContext(ctx, "SELECT share, user, mode FROM share_access ORDER BY share")
	if err != nil {
		return nil, err
	}
	defer grants.Close()
	for grants.Next() {
		var share, user, mode string
		if err := grants.Scan(&share, &user, &mode); err != nil {
			return nil, err
		}
		if i, ok := index[user]; ok {
			out[i].Shares = append(out[i].Shares, ShareAccess{Share: share, Mode: mode})
		}
	}
	return out, grants.Err()
}

// CreateShare records a share and its grants atomically.
func (s *Store) CreateShare(ctx context.Context, sh Share) (Share, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Share{}, err
	}
	defer tx.Rollback()
	sh.CreatedAt = s.now().Unix()
	if _, err := tx.ExecContext(ctx, "INSERT INTO shares (name, pool, created_at, time_machine, tm_max_gib, recycle, recycle_days) VALUES (?, ?, ?, ?, ?, ?, ?)",
		sh.Name, sh.Pool, sh.CreatedAt, sh.TimeMachine, sh.TimeMachineMaxGiB, sh.Recycle, sh.RecycleDays); err != nil {
		if isUniqueViolation(err) {
			return Share{}, ErrExists
		}
		return Share{}, err
	}
	if err := insertAccess(ctx, tx, sh.Name, sh.Access); err != nil {
		return Share{}, err
	}
	if sh.Access == nil {
		sh.Access = []ShareAccess{}
	}
	return sh, tx.Commit()
}

// SetShareRecycle changes the recycle bin settings of a share.
func (s *Store) SetShareRecycle(ctx context.Context, share string, enabled bool, days int) error {
	res, err := s.db.ExecContext(ctx, "UPDATE shares SET recycle = ?, recycle_days = ? WHERE name = ?", enabled, days, share)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetShareTimeMachine changes the Time Machine settings of a share.
func (s *Store) SetShareTimeMachine(ctx context.Context, share string, enabled bool, maxGiB int) error {
	res, err := s.db.ExecContext(ctx, "UPDATE shares SET time_machine = ?, tm_max_gib = ? WHERE name = ?", enabled, maxGiB, share)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetShareAccess replaces the grants of a share.
func (s *Store) SetShareAccess(ctx context.Context, share string, access []ShareAccess) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM shares WHERE name = ?", share).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM share_access WHERE share = ?", share); err != nil {
		return err
	}
	if err := insertAccess(ctx, tx, share, access); err != nil {
		return err
	}
	return tx.Commit()
}

func insertAccess(ctx context.Context, tx *sql.Tx, share string, access []ShareAccess) error {
	for _, a := range access {
		_, err := tx.ExecContext(ctx, "INSERT INTO share_access (share, user, mode) VALUES (?, ?, ?)", share, a.User, a.Mode)
		if err != nil {
			if isUniqueViolation(err) || isForeignKeyViolation(err) {
				return ErrInvalidData
			}
			return err
		}
	}
	return nil
}

// Shares lists shares with their grants.
func (s *Store) Shares(ctx context.Context) ([]Share, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT name, pool, created_at, time_machine, tm_max_gib, recycle, recycle_days FROM shares ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Share{}
	index := map[string]int{}
	for rows.Next() {
		sh := Share{Access: []ShareAccess{}}
		if err := rows.Scan(&sh.Name, &sh.Pool, &sh.CreatedAt, &sh.TimeMachine, &sh.TimeMachineMaxGiB, &sh.Recycle, &sh.RecycleDays); err != nil {
			return nil, err
		}
		index[sh.Name] = len(out)
		out = append(out, sh)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	grants, err := s.db.QueryContext(ctx, "SELECT share, user, mode FROM share_access ORDER BY user")
	if err != nil {
		return nil, err
	}
	defer grants.Close()
	for grants.Next() {
		var share, user, mode string
		if err := grants.Scan(&share, &user, &mode); err != nil {
			return nil, err
		}
		if i, ok := index[share]; ok {
			out[i].Access = append(out[i].Access, ShareAccess{User: user, Mode: mode})
		}
	}
	return out, grants.Err()
}

// ShareByName returns one share or ErrNotFound.
func (s *Store) ShareByName(ctx context.Context, name string) (Share, error) {
	all, err := s.Shares(ctx)
	if err != nil {
		return Share{}, err
	}
	for _, sh := range all {
		if sh.Name == name {
			return sh, nil
		}
	}
	return Share{}, ErrNotFound
}

func isForeignKeyViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "FOREIGN KEY constraint failed")
}

// DeleteShare removes a share and its grants. ErrNotFound if unknown.
func (s *Store) DeleteShare(ctx context.Context, name string) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM shares WHERE name = ?", name) // grants: ON DELETE CASCADE
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
