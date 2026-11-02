// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "cwnsb.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestFirstAdminIsAtomic(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	var mu sync.Mutex
	created, refused := 0, 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.CreateFirstAdmin(ctx, "admin", "hash", "en")
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				created++
			case errors.Is(err, ErrSetupDone):
				refused++
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if created != 1 || refused != 7 {
		t.Fatalf("created=%d refused=%d, want 1/7", created, refused)
	}
	if n, _ := s.UserCount(ctx); n != 1 {
		t.Fatalf("users = %d", n)
	}
}

func TestSessions(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	u, err := s.CreateFirstAdmin(ctx, "admin", "hash", "fr")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSession(ctx, "h1", u.ID, time.Hour, "192.168.1.20", "test"); err != nil {
		t.Fatal(err)
	}
	got, err := s.SessionUser(ctx, "h1")
	if err != nil || got.Username != "admin" || got.Language != "fr" {
		t.Fatalf("SessionUser = %+v, %v", got, err)
	}
	if _, err := s.SessionUser(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown session: %v", err)
	}

	// Expiry: move the clock past the TTL.
	s.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	if _, err := s.SessionUser(ctx, "h1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired session still valid: %v", err)
	}
	if n, err := s.DeleteExpiredSessions(ctx); err != nil || n != 1 {
		t.Fatalf("purge = %d, %v", n, err)
	}
}

func TestSettingsAndAudit(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	var v struct{ Step string }
	if err := s.GetSetting(ctx, "onboarding", &v); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing setting: %v", err)
	}
	if err := s.SetSetting(ctx, "onboarding", map[string]string{"Step": "disks"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting(ctx, "onboarding", map[string]string{"Step": "done"}); err != nil {
		t.Fatal(err)
	}
	if err := s.GetSetting(ctx, "onboarding", &v); err != nil || v.Step != "done" {
		t.Fatalf("setting = %+v, %v", v, err)
	}

	for _, action := range []string{"auth.login", "system.identity"} {
		if err := s.Audit(ctx, AuditEntry{Actor: "admin", Action: action, Result: "ok"}); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := s.AuditList(ctx, 10)
	if err != nil || len(entries) != 2 || entries[0].Action != "system.identity" {
		t.Fatalf("audit = %+v, %v", entries, err)
	}
}

func TestReopenKeepsData(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(dir, "cwnsb.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateFirstAdmin(ctx, "admin", "hash", "en"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(ctx, filepath.Join(dir, "cwnsb.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if n, _ := s.UserCount(ctx); n != 1 {
		t.Fatalf("users after reopen = %d", n)
	}
}

func TestPools(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	p := Pool{Name: "tank", Layout: "mirror", UUID: "u-1", Members: []string{"wwn-0xa", "wwn-0xb"}}
	if _, err := s.CreatePool(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreatePool(ctx, p); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate: %v", err)
	}
	got, err := s.PoolByName(ctx, "tank")
	if err != nil || got.Layout != "mirror" || len(got.Members) != 2 || got.CreatedAt == 0 {
		t.Fatalf("pool = %+v, %v", got, err)
	}
	if _, err := s.PoolByName(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	all, err := s.Pools(ctx)
	if err != nil || len(all) != 1 {
		t.Fatalf("pools = %v, %v", all, err)
	}
}

func TestSharesAndUsers(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if _, err := s.CreatePool(ctx, Pool{Name: "tank", Layout: "mirror", UUID: "u", Members: []string{"a", "b"}}); err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{"alice", "bob"} {
		if _, err := s.CreateNASUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CreateNASUser(ctx, "alice"); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate user: %v", err)
	}
	sh, err := s.CreateShare(ctx, Share{Name: "photos", Pool: "tank", Access: []ShareAccess{{User: "alice", Mode: "rw"}, {User: "bob", Mode: "ro"}}})
	if err != nil || sh.CreatedAt == 0 {
		t.Fatalf("share = %+v, %v", sh, err)
	}
	// Unknown user, unknown pool, bad mode: all refused by the schema.
	if _, err := s.CreateShare(ctx, Share{Name: "x", Pool: "tank", Access: []ShareAccess{{User: "carol", Mode: "rw"}}}); err == nil {
		t.Error("unknown user accepted")
	}
	if _, err := s.CreateShare(ctx, Share{Name: "y", Pool: "nopool"}); err == nil {
		t.Error("unknown pool accepted")
	}
	if err := s.SetShareAccess(ctx, "photos", []ShareAccess{{User: "bob", Mode: "admin"}}); err == nil {
		t.Error("bad mode accepted")
	}

	users, err := s.NASUsers(ctx)
	if err != nil || len(users) != 2 || len(users[0].Shares) != 1 || users[0].Shares[0].Share != "photos" || users[0].Shares[0].Mode != "rw" {
		t.Fatalf("users = %+v, %v", users, err)
	}
	// Deleting a user removes their grants.
	if err := s.DeleteNASUser(ctx, "bob"); err != nil {
		t.Fatal(err)
	}
	got, err := s.ShareByName(ctx, "photos")
	if err != nil || len(got.Access) != 1 || got.Access[0].User != "alice" {
		t.Fatalf("after delete = %+v, %v", got, err)
	}
	if err := s.SetShareAccess(ctx, "photos", nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.ShareByName(ctx, "photos"); len(got.Access) != 0 {
		t.Fatalf("access not cleared: %+v", got)
	}
}

func TestAlerts(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	clock := time.Unix(1_790_000_000, 0)
	s.now = func() time.Time { return clock }
	if err := s.RaiseAlert(ctx, "pool.degraded:tank", "crit", "pool.degraded", map[string]any{"pool": "tank"}); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(time.Hour)
	s.TouchAlert(ctx, "pool.degraded:tank")
	if err := s.AckAlert(ctx, "pool.degraded:tank"); err != nil {
		t.Fatal(err)
	}
	if err := s.AckAlert(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ack unknown: %v", err)
	}
	active, _ := s.ActiveAlerts(ctx)
	if active["pool.degraded:tank"] != "crit" {
		t.Fatalf("active = %v", active)
	}
	resolved, err := s.ResolveAlert(ctx, "pool.degraded:tank")
	if err != nil || resolved.Params["pool"] != "tank" {
		t.Fatalf("resolve = %+v %v", resolved, err)
	}
	if active, _ := s.ActiveAlerts(ctx); len(active) != 0 {
		t.Fatalf("still active: %v", active)
	}
	// It comes back later: a fresh alert, not acknowledged.
	clock = clock.Add(24 * time.Hour)
	s.RaiseAlert(ctx, "pool.degraded:tank", "crit", "pool.degraded", map[string]any{"pool": "tank"})
	list, _ := s.Alerts(ctx, 0)
	if len(list) != 1 || list[0].AckedAt != 0 || list[0].FirstSeen != clock.Unix() || list[0].ResolvedAt != 0 {
		t.Fatalf("reappeared = %+v", list)
	}
	if lang := s.AdminLanguage(ctx); lang != "en" {
		t.Fatalf("no admin yet: %s", lang)
	}
	s.CreateFirstAdmin(ctx, "admin", "h", "fr")
	if lang := s.AdminLanguage(ctx); lang != "fr" {
		t.Fatalf("admin language = %s", lang)
	}
}

func TestExportAndPendingRestore(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	// The NAS that was backed up: an admin named "old".
	src, err := Open(ctx, filepath.Join(dir, "old.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := src.CreateFirstAdmin(ctx, "old", "hash", "fr"); err != nil {
		t.Fatal(err)
	}
	export := filepath.Join(dir, "backup", "config.db")
	if err := src.ExportTo(ctx, export); err != nil {
		t.Fatal(err)
	}
	if err := src.ExportTo(ctx, export); err != nil { // replaced, not refused
		t.Fatalf("second export: %v", err)
	}
	src.Close()

	// The new system has its own admin; the backup is pending.
	dbPath := filepath.Join(dir, "cwnsb.db")
	cur, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := cur.CreateFirstAdmin(ctx, "new", "hash", "en")
	if _, err := cur.CreateSession(ctx, "tokenhash", u.ID, time.Hour, "127.0.0.1", "test"); err != nil {
		t.Fatal(err)
	}
	cur.Close()
	pending := filepath.Join(dir, "restore", "config.db")
	os.MkdirAll(filepath.Dir(pending), 0o750)
	b, _ := os.ReadFile(export)
	os.WriteFile(pending, b, 0o600)

	done, err := ApplyPendingRestore(dbPath, pending, time.Now())
	if err != nil || !done {
		t.Fatalf("restore = %v %v", done, err)
	}
	if _, err := os.Stat(pending); !os.IsNotExist(err) {
		t.Fatal("pending file left: it would be applied again")
	}
	matches, _ := filepath.Glob(dbPath + ".before-restore-*")
	if len(matches) != 1 {
		t.Fatalf("previous database not kept: %v", matches)
	}
	got, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer got.Close()
	if _, err := got.UserByUsername(ctx, "old"); err != nil {
		t.Fatalf("restored admin: %v", err)
	}
	if err := got.PurgeSessions(ctx); err != nil {
		t.Fatal(err)
	}
	if done, err := ApplyPendingRestore(dbPath, pending, time.Now()); done || err != nil {
		t.Fatalf("nothing pending = %v %v", done, err)
	}
	os.WriteFile(pending, []byte("root:x:0:0"), 0o600)
	if done, err := ApplyPendingRestore(dbPath, pending, time.Now()); done || err == nil {
		t.Fatal("a non-SQLite file must be refused")
	}
}
