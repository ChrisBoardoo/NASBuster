// SPDX-License-Identifier: AGPL-3.0-or-later

package apps

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// testdata/portainer-2.45.2.log: `docker compose logs --no-color --timestamps`
// on the test Pi 5 (2026-10-12), tokens replaced. A first start that locked
// itself 5 minutes later, then a restart with a new token.
func readPortainerLog(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("testdata/portainer-2.45.2.log")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParseSetupTokenRealLogs(t *testing.T) {
	logs := readPortainerLog(t)
	tok, ok := ParseSetupToken(logs)
	if !ok || tok.Token != strings.Repeat("2", 64) || tok.TimedOut {
		t.Fatalf("after the restart: %+v %v", tok, ok)
	}
	if want := time.Date(2026, 10, 12, 7, 3, 2, 919345523, time.UTC); !tok.IssuedAt.Equal(want) {
		t.Fatalf("issued at %v, want %v", tok.IssuedAt, want)
	}

	// Before the restart: the first token, then the lockout 5 minutes later.
	first := logs[:strings.Index(logs, "07:03:02")]
	tok, ok = ParseSetupToken(first)
	if !ok || tok.Token != strings.Repeat("1", 64) || !tok.TimedOut {
		t.Fatalf("first start: %+v %v", tok, ok)
	}
	if lived := time.Date(2026, 10, 12, 6, 46, 25, 0, time.UTC).Sub(tok.IssuedAt); lived > SetupTokenLifetime || lived < SetupTokenLifetime-time.Second {
		t.Fatalf("lockout %v after the token", lived)
	}
}

func TestParseSetupTokenNone(t *testing.T) {
	for _, logs := range []string{
		"",
		"uptime-kuma-1  | 2026-10-12T06:20:00Z Welcome to Uptime Kuma\n",
		"portainer-1  | 2026-10-12T06:41:25Z setup_token=xyz\n",                                   // not hex
		"portainer-1  | 2026-10-12T06:41:25Z the instance timed out for security purposes\n",      // lockout without a token
		"portainer-1  | 2026-10-12T06:41:25Z note: setup_token=" + strings.Repeat("a", 64) + "\n", // not the token line
	} {
		if tok, ok := ParseSetupToken(logs); ok {
			t.Errorf("%q: found %+v", logs, tok)
		}
	}
	// Without timestamps (plain `docker logs`), the token is still found.
	tok, ok := ParseSetupToken("setup_token=" + strings.Repeat("ab", 32) + "\r\n")
	if !ok || tok.Token != strings.Repeat("ab", 32) || !tok.IssuedAt.IsZero() {
		t.Fatalf("plain: %+v %v", tok, ok)
	}
}

func TestAdminExists(t *testing.T) {
	status := http.StatusNotFound
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != AdminCheckPath || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
	}))
	defer srv.Close()
	_, portText, _ := net.SplitHostPort(srv.Listener.Addr().String())
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if exists, err := AdminExists(ctx, "https", port); err != nil || exists {
		t.Fatalf("404: %v %v", exists, err)
	}
	status = http.StatusNoContent
	if exists, err := AdminExists(ctx, "https", port); err != nil || !exists {
		t.Fatalf("204: %v %v", exists, err)
	}
	status = http.StatusServiceUnavailable
	if _, err := AdminExists(ctx, "https", port); err == nil {
		t.Fatal("503 accepted")
	}
	for _, bad := range []struct {
		scheme string
		port   int
	}{{"ftp", port}, {"https", 0}, {"https", 70000}} {
		if _, err := AdminExists(ctx, bad.scheme, bad.port); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
}
