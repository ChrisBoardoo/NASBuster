// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/crosswax/nasbuster/internal/auth"
	"github.com/crosswax/nasbuster/internal/store"
)

// setupCodes holds the first-run code. It lives in a root/cwnsb-only file so it
// survives restarts until the first admin is created, then the file is removed.
type setupCodes struct {
	mu     sync.Mutex
	code   string
	path   string
	logger *slog.Logger
}

func newSetupCodes(ctx context.Context, st *store.Store, path string, logger *slog.Logger) (*setupCodes, error) {
	s := &setupCodes{path: path, logger: logger}
	n, err := st.UserCount(ctx)
	if err != nil {
		return nil, err
	}
	if n > 0 {
		_ = os.Remove(path) // leftover from an interrupted setup
		return s, nil
	}
	if b, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(b))) == 9 {
		s.code = strings.TrimSpace(string(b))
	} else {
		s.code = auth.NewSetupCode()
		if err := os.WriteFile(path, []byte(s.code+"\n"), 0o600); err != nil {
			return nil, err
		}
	}
	logger.Warn("first run: open the web UI and sign in with this machine's account (sudo group), or enter this setup code",
		"setup_code", s.code, "file", path)
	return s, nil
}

func (s *setupCodes) Current() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.code
}

func (s *setupCodes) Consume() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.code = ""
	if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		s.logger.Warn("could not remove setup code file", "err", err)
	}
}
