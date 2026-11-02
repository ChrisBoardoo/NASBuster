// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

// User is an account. PasswordHash never leaves the server.
type User struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	PasswordHash string `json:"-"`
	Role         string `json:"role"`
	Language     string `json:"language"`
	CreatedAt    int64  `json:"createdAt"`
}

const userColumns = "id, username, password_hash, role, language, created_at"

func scanUser(row interface{ Scan(...any) error }) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.Language, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return u, ErrNotFound
	}
	return u, err
}

// UserCount returns the number of accounts.
func (s *Store) UserCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM users").Scan(&n)
	return n, err
}

// CreateFirstAdmin creates the initial administrator. It fails with
// ErrSetupDone if any account exists, atomically: two concurrent setup
// requests cannot both succeed.
func (s *Store) CreateFirstAdmin(ctx context.Context, username, passwordHash, language string) (User, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM users").Scan(&n); err != nil {
		return User{}, err
	}
	if n > 0 {
		return User{}, ErrSetupDone
	}
	now := s.now().Unix()
	res, err := tx.ExecContext(ctx,
		"INSERT INTO users (username, password_hash, role, language, created_at, updated_at) VALUES (?, ?, 'admin', ?, ?, ?)",
		username, passwordHash, language, now, now)
	if err != nil {
		return User{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return User{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, err
	}
	return User{ID: id, Username: username, PasswordHash: passwordHash, Role: "admin", Language: language, CreatedAt: now}, nil
}

// UserByUsername looks an account up by name.
func (s *Store) UserByUsername(ctx context.Context, username string) (User, error) {
	return scanUser(s.db.QueryRowContext(ctx, "SELECT "+userColumns+" FROM users WHERE username = ?", username))
}

// UserByID looks an account up by id.
func (s *Store) UserByID(ctx context.Context, id int64) (User, error) {
	return scanUser(s.db.QueryRowContext(ctx, "SELECT "+userColumns+" FROM users WHERE id = ?", id))
}

// SetUserLanguage updates the UI language of an account.
func (s *Store) SetUserLanguage(ctx context.Context, id int64, language string) error {
	res, err := s.db.ExecContext(ctx, "UPDATE users SET language = ?, updated_at = ? WHERE id = ?", language, s.now().Unix(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Admins lists the administrator accounts, oldest first.
func (s *Store) Admins(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+userColumns+" FROM users WHERE role = 'admin' ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// SetUserPassword replaces the password hash of an account (ADR-0037).
func (s *Store) SetUserPassword(ctx context.Context, id int64, passwordHash string) error {
	res, err := s.db.ExecContext(ctx, "UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?", passwordHash, s.now().Unix(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteUserSessions revokes every session of an account except the one
// whose token hash is keep ("" keeps none), and returns how many went.
func (s *Store) DeleteUserSessions(ctx context.Context, userID int64, keep string) (int64, error) {
	res, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE user_id = ? AND token_hash <> ?", userID, keep)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Session is a login. Only the SHA-256 of the token is stored.
type Session struct {
	TokenHash string
	UserID    int64
	CreatedAt int64
	ExpiresAt int64
}

// CreateSession stores a new session.
func (s *Store) CreateSession(ctx context.Context, tokenHash string, userID int64, ttl time.Duration, remoteAddr, userAgent string) (Session, error) {
	now := s.now().Unix()
	sess := Session{TokenHash: tokenHash, UserID: userID, CreatedAt: now, ExpiresAt: now + int64(ttl/time.Second)}
	if len(userAgent) > 256 {
		userAgent = userAgent[:256]
	}
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO sessions (token_hash, user_id, created_at, expires_at, last_seen_at, remote_addr, user_agent) VALUES (?, ?, ?, ?, ?, ?, ?)",
		sess.TokenHash, userID, now, sess.ExpiresAt, now, remoteAddr, userAgent)
	if isUniqueViolation(err) {
		return Session{}, ErrExists
	}
	return sess, err
}

// SessionUser resolves a valid (non-expired) session to its user.
func (s *Store) SessionUser(ctx context.Context, tokenHash string) (User, error) {
	now := s.now().Unix()
	u, err := scanUser(s.db.QueryRowContext(ctx,
		"SELECT u.id, u.username, u.password_hash, u.role, u.language, u.created_at FROM sessions s JOIN users u ON u.id = s.user_id WHERE s.token_hash = ? AND s.expires_at > ?",
		tokenHash, now))
	if err != nil {
		return User{}, err
	}
	// Touch at most once a minute to keep writes rare.
	_, _ = s.db.ExecContext(ctx, "UPDATE sessions SET last_seen_at = ? WHERE token_hash = ? AND last_seen_at < ?", now, tokenHash, now-60)
	return u, nil
}

// DeleteSession revokes a session.
func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash = ?", tokenHash)
	return err
}

// DeleteExpiredSessions purges expired sessions.
func (s *Store) DeleteExpiredSessions(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at <= ?", s.now().Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// GetSetting decodes the JSON value of key into v. Missing keys return ErrNotFound.
func (s *Store) GetSetting(ctx context.Context, key string, v any) error {
	var raw string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(raw), v)
}

// SetSetting stores v as JSON under key.
func (s *Store) SetSetting(ctx context.Context, key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		"INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at",
		key, string(raw), s.now().Unix())
	return err
}

// settingInstallation holds the installation id (P6).
const settingInstallation = "installation.id"

// InstallationID returns the id of this installation, drawn at random on
// first use. It lives in the database, so a restored configuration brings
// back the id of the installation it continues; a fresh SD card gets a new
// one. Backups carry it, and an installation only rotates its own.
func (s *Store) InstallationID(ctx context.Context) (string, error) {
	var id string
	err := s.GetSetting(ctx, settingInstallation, &id)
	if err == nil && id != "" {
		return id, nil
	}
	if err != nil && !errors.Is(err, ErrNotFound) {
		return "", err
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	raw, _ := json.Marshal(hex.EncodeToString(b))
	// Two first callers at once: the first insert wins, both read it back.
	if _, err := s.db.ExecContext(ctx, "INSERT OR IGNORE INTO settings (key, value, updated_at) VALUES (?, ?, ?)",
		settingInstallation, string(raw), s.now().Unix()); err != nil {
		return "", err
	}
	err = s.GetSetting(ctx, settingInstallation, &id)
	return id, err
}

// AuditEntry is one line of the audit log. Never put secrets in it.
type AuditEntry struct {
	ID         int64  `json:"id"`
	TS         int64  `json:"ts"`
	Actor      string `json:"actor"`
	Action     string `json:"action"`
	Target     string `json:"target"`
	Result     string `json:"result"`
	Detail     string `json:"detail"`
	RemoteAddr string `json:"remoteAddr"`
}

// Audit appends an entry. TS is filled in when zero.
func (s *Store) Audit(ctx context.Context, e AuditEntry) error {
	if e.TS == 0 {
		e.TS = s.now().Unix()
	}
	if len(e.Detail) > 500 {
		e.Detail = e.Detail[:500]
	}
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO audit_log (ts, actor, action, target, result, detail, remote_addr) VALUES (?, ?, ?, ?, ?, ?, ?)",
		e.TS, e.Actor, e.Action, e.Target, e.Result, e.Detail, e.RemoteAddr)
	return err
}

// AuditList returns the newest entries first.
func (s *Store) AuditList(ctx context.Context, limit int) ([]AuditEntry, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, ts, actor, action, target, result, detail, remote_addr FROM audit_log ORDER BY id DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.TS, &e.Actor, &e.Action, &e.Target, &e.Result, &e.Detail, &e.RemoteAddr); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
