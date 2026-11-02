// SPDX-License-Identifier: AGPL-3.0-or-later

// Package store persists nasbusterd state in SQLite (pure Go driver, no cgo).
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// Errors.
var (
	ErrNotFound    = errors.New("not found")
	ErrExists      = errors.New("already exists")
	ErrSetupDone   = errors.New("setup already done")
	ErrInvalidData = errors.New("invalid data")
)

// Store wraps the database.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

// migrations are applied in order; PRAGMA user_version records progress.
// Never edit an existing entry: append a new one.
var migrations = []string{
	`CREATE TABLE users (
		id            INTEGER PRIMARY KEY,
		username      TEXT    NOT NULL UNIQUE,
		password_hash TEXT    NOT NULL,
		role          TEXT    NOT NULL DEFAULT 'admin',
		language      TEXT    NOT NULL DEFAULT 'en',
		created_at    INTEGER NOT NULL,
		updated_at    INTEGER NOT NULL
	);
	CREATE TABLE sessions (
		token_hash   TEXT    PRIMARY KEY,
		user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		created_at   INTEGER NOT NULL,
		expires_at   INTEGER NOT NULL,
		last_seen_at INTEGER NOT NULL,
		remote_addr  TEXT    NOT NULL DEFAULT '',
		user_agent   TEXT    NOT NULL DEFAULT ''
	);
	CREATE INDEX sessions_user ON sessions(user_id);
	CREATE TABLE settings (
		key        TEXT    PRIMARY KEY,
		value      TEXT    NOT NULL,
		updated_at INTEGER NOT NULL
	);
	CREATE TABLE audit_log (
		id          INTEGER PRIMARY KEY,
		ts          INTEGER NOT NULL,
		actor       TEXT    NOT NULL,
		action      TEXT    NOT NULL,
		target      TEXT    NOT NULL DEFAULT '',
		result      TEXT    NOT NULL,
		detail      TEXT    NOT NULL DEFAULT '',
		remote_addr TEXT    NOT NULL DEFAULT ''
	);
	CREATE INDEX audit_ts ON audit_log(ts);`,
	// 2: storage pools (desired state, ADR-0005/0008).
	`CREATE TABLE pools (
		name       TEXT    PRIMARY KEY,
		layout     TEXT    NOT NULL,
		uuid       TEXT    NOT NULL,
		md_device  TEXT    NOT NULL DEFAULT '',
		members    TEXT    NOT NULL,
		created_at INTEGER NOT NULL
	);`,
	// 3: NAS users and SMB shares (ADR-0009). No password is ever stored here.
	`CREATE TABLE nas_users (
		name       TEXT    PRIMARY KEY,
		created_at INTEGER NOT NULL
	);
	CREATE TABLE shares (
		name       TEXT    PRIMARY KEY,
		pool       TEXT    NOT NULL REFERENCES pools(name),
		created_at INTEGER NOT NULL
	);
	CREATE TABLE share_access (
		share TEXT NOT NULL REFERENCES shares(name) ON DELETE CASCADE,
		user  TEXT NOT NULL REFERENCES nas_users(name) ON DELETE CASCADE,
		mode  TEXT NOT NULL CHECK (mode IN ('rw', 'ro')),
		PRIMARY KEY (share, user)
	);`,
	// 4: alerts (ADR-0011). One row per stable alert id; resolved_at = 0 while active.
	`CREATE TABLE alerts (
		id          TEXT    PRIMARY KEY,
		level       TEXT    NOT NULL,
		code        TEXT    NOT NULL,
		params      TEXT    NOT NULL,
		first_seen  INTEGER NOT NULL,
		last_seen   INTEGER NOT NULL,
		resolved_at INTEGER NOT NULL DEFAULT 0,
		acked_at    INTEGER NOT NULL DEFAULT 0
	);`,
	// 5: applications (ADR-0013). The compose file is the desired state.
	`CREATE TABLE apps (
		name       TEXT    PRIMARY KEY,
		pool       TEXT    NOT NULL REFERENCES pools(name),
		template   TEXT    NOT NULL DEFAULT '',
		compose    TEXT    NOT NULL,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);`,
	// 6: Time Machine shares (M4b).
	`ALTER TABLE shares ADD COLUMN time_machine INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE shares ADD COLUMN tm_max_gib INTEGER NOT NULL DEFAULT 0;`,
	// 7: share recycle bins.
	`ALTER TABLE shares ADD COLUMN recycle INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE shares ADD COLUMN recycle_days INTEGER NOT NULL DEFAULT 0;`,
	// 8: event journal (ADR-0031). Written only when something happens, capped.
	`CREATE TABLE events (
		id        INTEGER PRIMARY KEY,
		ts        INTEGER NOT NULL,
		ts_state  TEXT    NOT NULL,
		boot_id   TEXT    NOT NULL DEFAULT '',
		uptime_ms INTEGER NOT NULL DEFAULT 0,
		level     TEXT    NOT NULL,
		code      TEXT    NOT NULL,
		phase     TEXT    NOT NULL DEFAULT '',
		subject   TEXT    NOT NULL DEFAULT '',
		source    TEXT    NOT NULL DEFAULT '',
		params    TEXT    NOT NULL DEFAULT '{}'
	);
	CREATE INDEX events_ts ON events(ts);
	CREATE INDEX events_uncertain ON events(boot_id, ts_state);`,
}

// Open opens (or creates) the database at path and applies migrations.
// path ":memory:" gives a private in-memory database (tests).
func Open(ctx context.Context, path string) (*Store, error) {
	dsn := "file:" + url.PathEscape(path) + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	if path == ":memory:" {
		dsn = "file::memory:?_pragma=foreign_keys(1)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection: SQLite serialises writes anyway, and it keeps an
	// in-memory database alive and shared.
	db.SetMaxOpenConns(1)
	s := &Store{db: db, now: time.Now}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	var version int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if version > len(migrations) {
		return fmt.Errorf("database schema v%d is newer than this binary (v%d)", version, len(migrations))
	}
	for i := version; i < len(migrations); i++ {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// ExportTo writes a consistent copy of the database to path (replaced if it
// exists), for the configuration backups (ADR-0014).
func (s *Store) ExportTo(ctx context.Context, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_, err := s.db.ExecContext(ctx, "VACUUM INTO ?", path)
	return err
}

// ApplyPendingRestore swaps the database at pending (left by the helper's
// config.restore) in for the one at dbPath, before Open. The previous
// database is kept next to it. It reports whether a restore happened.
func ApplyPendingRestore(dbPath, pending string, now time.Time) (bool, error) {
	b, err := os.ReadFile(pending)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !strings.HasPrefix(string(b), "SQLite format 3\x00") {
		os.Remove(pending)
		return false, fmt.Errorf("pending restore %s is not an SQLite database", pending)
	}
	keep := dbPath + ".before-restore-" + now.UTC().Format("20060102T150405Z")
	if err := os.Rename(dbPath, keep); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		os.Remove(dbPath + suffix)
	}
	if err := os.WriteFile(dbPath, b, 0o600); err != nil {
		return false, err
	}
	return true, os.Remove(pending)
}

// PurgeSessions deletes every login session (after a restore, the sessions
// of the backup must not come back to life).
func (s *Store) PurgeSessions(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM sessions")
	return err
}
