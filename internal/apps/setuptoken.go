// SPDX-License-Identifier: AGPL-3.0-or-later

package apps

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Portainer's setup token (verified with 2.45.2, tag lts, 2026-10-12). At
// every start, Portainer prints a new one-time token that its setup screen
// asks for, and locks itself 5 minutes later if no admin account exists
// (only a restart unlocks it, with a new token). The administrator still
// creates that account in Portainer, with the name and password they choose:
// CWNSB only finds the token in the logs so that nobody has to read them.

// SetupTokenLifetime is how long after its start Portainer waits for its
// admin account before locking itself.
const SetupTokenLifetime = 5 * time.Minute

// AdminCheckPath answers 204 once Portainer's admin account exists, 404
// before (no authentication).
const AdminCheckPath = "/api/users/admin/check"

// SetupToken is the last setup token found in an application's logs.
type SetupToken struct {
	Token    string
	IssuedAt time.Time // zero when the logs carry no timestamps
	TimedOut bool      // the instance locked itself after printing it
}

var (
	ansiRe       = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)
	setupTokenRe = regexp.MustCompile(`^setup_token=([0-9a-f]{32,128})$`)
)

const lockoutMarker = "timed out for security purposes"

// ParseSetupToken reads `docker compose logs --no-color --timestamps`
// ("portainer-1  | 2026-10-12T07:03:02.919345523Z setup_token=…"; Portainer
// colors its own lines and Compose starts each one with an erase sequence).
// ok is false when no token was printed.
func ParseSetupToken(logs string) (tok SetupToken, ok bool) {
	for _, line := range strings.Split(logs, "\n") {
		line = ansiRe.ReplaceAllString(strings.TrimRight(line, "\r"), "")
		if _, rest, found := strings.Cut(line, " | "); found { // the service prefix comes first
			line = rest
		}
		var at time.Time
		if ts, rest, _ := strings.Cut(line, " "); ts != "" {
			if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
				at, line = t, rest
			}
		}
		line = strings.TrimSpace(line)
		if m := setupTokenRe.FindStringSubmatch(line); m != nil {
			tok, ok = SetupToken{Token: m[1], IssuedAt: at}, true
		} else if ok && strings.Contains(line, lockoutMarker) {
			tok.TimedOut = true
		}
	}
	return tok, ok
}

// AdminExists asks a Portainer published on this host whether its admin
// account exists. Only 127.0.0.1 is asked and nothing secret is sent, so its
// self-signed certificate is not verified.
func AdminExists(ctx context.Context, scheme string, port int) (bool, error) {
	if scheme != "http" && scheme != "https" || port < 1 || port > 65535 {
		return false, fmt.Errorf("invalid address %s:%d", scheme, port)
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s://127.0.0.1:%d%s", scheme, port, AdminCheckPath), nil)
	if err != nil {
		return false, err
	}
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // loopback, no credential sent
			Proxy:           nil,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	defer client.CloseIdleConnections()
	res, err := client.Do(req)
	if err != nil {
		return false, err
	}
	res.Body.Close()
	switch res.StatusCode {
	case http.StatusNoContent:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	}
	return false, fmt.Errorf("admin check: HTTP %d", res.StatusCode)
}
