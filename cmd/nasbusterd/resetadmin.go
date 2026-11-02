// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"

	"github.com/crosswax/nasbuster/internal/auth"
	"github.com/crosswax/nasbuster/internal/store"
	"github.com/crosswax/nasbuster/internal/validate"
)

// serviceUser owns the daemon's state (packaging/sysusers).
const serviceUser = "cwnsb"

// resetAdmin is `sudo nasbusterd -reset-admin [name]` (ADR-0037): the console
// way to set a forgotten admin password, as omv-firstaid does on OMV. Root
// proves ownership of the machine; the password is typed on the terminal,
// never passed as an argument (shell history, ps).
func resetAdmin(dataDir, name string) error {
	// The database stays owned by the daemon's account.
	if err := becomeServiceUser(); err != nil {
		return err
	}
	dbPath := filepath.Join(dataDir, "cwnsb.db")
	if _, err := os.Stat(dbPath); err != nil {
		return fmt.Errorf("no NasBuster database in %s: %w", dataDir, err)
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dbPath) // WAL with a busy timeout: the daemon may run
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer st.Close()

	user, err := pickAdmin(ctx, st, name)
	if err != nil {
		return err
	}
	password, err := readNewPassword(passwordReader(os.Stdin, os.Stderr), user.Username)
	if err != nil {
		return err
	}
	closed, err := setAdminPassword(ctx, st, user, password)
	if err != nil {
		return err
	}
	fmt.Printf("Password of %s changed, %d sessions closed.\n", user.Username, closed)
	fmt.Println("If sign-in is locked after failed attempts, it reopens by itself within 15 minutes.")
	return nil
}

// pickAdmin returns the named administrator, or the only one.
func pickAdmin(ctx context.Context, st *store.Store, name string) (store.User, error) {
	admins, err := st.Admins(ctx)
	if err != nil {
		return store.User{}, err
	}
	if len(admins) == 0 {
		return store.User{}, errors.New("no administrator yet: finish the setup in the web interface")
	}
	names := make([]string, len(admins))
	for i, a := range admins {
		names[i] = a.Username
	}
	want := strings.ToLower(strings.TrimSpace(name))
	if want == "" {
		if len(admins) == 1 {
			return admins[0], nil
		}
		return store.User{}, fmt.Errorf("several administrators (%s): name one", strings.Join(names, ", "))
	}
	for _, a := range admins {
		if a.Username == want {
			return a, nil
		}
	}
	return store.User{}, fmt.Errorf("no administrator named %q (administrators: %s)", name, strings.Join(names, ", "))
}

// passwordReader reads without echo from a terminal, or one line at a time
// from a pipe (as chpasswd does).
func passwordReader(in *os.File, out io.Writer) func(prompt string) (string, error) {
	fd := int(in.Fd())
	if term.IsTerminal(fd) {
		return func(prompt string) (string, error) {
			fmt.Fprint(out, prompt)
			b, err := term.ReadPassword(fd)
			fmt.Fprintln(out)
			return string(b), err
		}
	}
	r := bufio.NewReader(in)
	return func(string) (string, error) {
		line, err := r.ReadString('\n')
		if err != nil && line == "" {
			return "", errors.New("no password on the standard input")
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
}

// readNewPassword asks twice and checks the password rules.
func readNewPassword(read func(prompt string) (string, error), username string) (string, error) {
	first, err := read(fmt.Sprintf("New password for %s: ", username))
	if err != nil {
		return "", err
	}
	if !validate.Password(first) {
		return "", fmt.Errorf("the password needs %d to %d characters: nothing changed", validate.PasswordMin, validate.PasswordMax)
	}
	again, err := read("Type it again: ")
	if err != nil {
		return "", err
	}
	if again != first {
		return "", errors.New("the two passwords differ: nothing changed")
	}
	return first, nil
}

// setAdminPassword stores the new password, ends every session of the
// account and writes the audit line. It returns how many sessions ended.
func setAdminPassword(ctx context.Context, st *store.Store, user store.User, password string) (int64, error) {
	hash, err := auth.HashPassword(password)
	if err != nil {
		return 0, err
	}
	if err := st.SetUserPassword(ctx, user.ID, hash); err != nil {
		return 0, err
	}
	closed, err := st.DeleteUserSessions(ctx, user.ID, "")
	if err != nil {
		return 0, err
	}
	return closed, st.Audit(ctx, store.AuditEntry{Actor: "console", Action: "auth.password_reset",
		Target: user.Username, Result: "ok", Detail: "console command"})
}
