// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/crosswax/nasbuster/internal/auth"
	"github.com/crosswax/nasbuster/internal/store"
)

func TestResetAdminFromTheConsole(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if _, err := pickAdmin(ctx, st, ""); err == nil || !strings.Contains(err.Error(), "no administrator yet") {
		t.Fatalf("no admin: %v", err)
	}
	hash, _ := auth.HashPassword("correct horse battery")
	admin, err := st.CreateFirstAdmin(ctx, "admin", hash, "fr")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateSession(ctx, "token-a", admin.ID, time.Hour, "", ""); err != nil {
		t.Fatal(err)
	}
	if u, err := pickAdmin(ctx, st, ""); err != nil || u.ID != admin.ID {
		t.Fatalf("only admin: %+v %v", u, err)
	}
	if u, err := pickAdmin(ctx, st, " Admin "); err != nil || u.ID != admin.ID {
		t.Fatalf("named admin: %+v %v", u, err)
	}
	if _, err := pickAdmin(ctx, st, "root"); err == nil || !strings.Contains(err.Error(), "administrators: admin") {
		t.Fatalf("unknown admin: %v", err)
	}

	answers := func(lines ...string) func(string) (string, error) {
		return func(string) (string, error) {
			line := lines[0]
			lines = lines[1:]
			return line, nil
		}
	}
	if _, err := readNewPassword(answers("short"), "admin"); err == nil {
		t.Fatal("short password accepted")
	}
	if _, err := readNewPassword(answers("a brand new passphrase", "a brand new passphrasE"), "admin"); err == nil {
		t.Fatal("two different passwords accepted")
	}
	password, err := readNewPassword(answers("a brand new passphrase", "a brand new passphrase"), "admin")
	if err != nil {
		t.Fatal(err)
	}

	closed, err := setAdminPassword(ctx, st, admin, password)
	if err != nil || closed != 1 {
		t.Fatalf("closed = %d, err = %v", closed, err)
	}
	u, _ := st.UserByID(ctx, admin.ID)
	if ok, _ := auth.VerifyPassword("a brand new passphrase", u.PasswordHash); !ok {
		t.Fatal("new password not stored")
	}
	if _, err := st.SessionUser(ctx, "token-a"); err == nil {
		t.Fatal("old session survived")
	}
	entries, _ := st.AuditList(ctx, 10)
	if len(entries) != 1 || entries[0].Actor != "console" || entries[0].Action != "auth.password_reset" || entries[0].Target != "admin" {
		t.Fatalf("audit = %+v", entries)
	}
}
