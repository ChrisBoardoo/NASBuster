// SPDX-License-Identifier: AGPL-3.0-or-later

package alerts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Channels configures where notifications go (setting "notifications").
type Channels struct {
	Ntfy    NtfyConfig    `json:"ntfy"`
	Webhook WebhookConfig `json:"webhook"`
}

// NtfyConfig: POST to https://ntfy.sh/<topic> or a self-hosted server.
type NtfyConfig struct {
	Enabled bool   `json:"enabled"`
	URL     string `json:"url"`
	Token   string `json:"token,omitempty"` // secret: never sent back by the API
}

// WebhookConfig: POST a JSON message (Home Assistant, Discord bridge, scripts).
type WebhookConfig struct {
	Enabled bool   `json:"enabled"`
	URL     string `json:"url"`
}

// Message is one notification.
type Message struct {
	NAS      string         `json:"nas"`
	Title    string         `json:"title"`
	Level    string         `json:"level"` // warn, crit, info
	Code     string         `json:"code"`
	Resolved bool           `json:"resolved"`
	Params   map[string]any `json:"params"`
	Time     int64          `json:"time"`
}

// ValidURL accepts https URLs, and plain http only towards the local
// network (a ntfy server on the LAN). Credentials in URLs are refused: they
// would end up in logs.
func ValidURL(raw string) error {
	if len(raw) > 512 {
		return errors.New("URL too long")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return errors.New("invalid URL")
	}
	if u.User != nil {
		return errors.New("credentials in the URL are not allowed: use the token field")
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if isLocalHost(u.Hostname()) {
			return nil
		}
		return errors.New("http is only allowed on the local network: use https")
	default:
		return errors.New("the URL must start with https://")
	}
}

// IsLocalURL reports whether a URL points to the local network.
func IsLocalURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && isLocalHost(u.Hostname())
}

func isLocalHost(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".lan") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast())
}

// Notifier sends messages over HTTP.
type Notifier struct {
	Client *http.Client
}

// NewNotifier returns a notifier with a short timeout that never follows
// redirects (a redirect could point anywhere).
func NewNotifier() *Notifier {
	return &Notifier{Client: &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// Result is the outcome per channel. Code and Status let the UI explain the
// failure in the user's language; Error is the technical text.
type Result struct {
	Channel string `json:"channel"`
	OK      bool   `json:"ok"`
	Code    string `json:"code,omitempty"`   // notify.http_status, notify.timeout, notify.unreachable, notify.invalid_url
	Status  int    `json:"status,omitempty"` // HTTP status for notify.http_status
	Error   string `json:"error,omitempty"`
}

// sendError is a classified delivery failure.
type sendError struct {
	code   string
	status int
	err    error
}

func (e *sendError) Error() string { return e.err.Error() }

// Send delivers m to every enabled channel.
func (n *Notifier) Send(ctx context.Context, ch Channels, m Message) []Result {
	var out []Result
	if ch.Ntfy.Enabled {
		out = append(out, n.result("ntfy", n.ntfy(ctx, ch.Ntfy, m)))
	}
	if ch.Webhook.Enabled {
		out = append(out, n.result("webhook", n.webhook(ctx, ch.Webhook, m)))
	}
	return out
}

func (n *Notifier) result(channel string, err error) Result {
	if err == nil {
		return Result{Channel: channel, OK: true}
	}
	r := Result{Channel: channel, Code: "notify.unreachable", Error: err.Error()}
	var se *sendError
	if errors.As(err, &se) {
		r.Code, r.Status = se.code, se.status
	}
	return r
}

func (n *Notifier) ntfy(ctx context.Context, c NtfyConfig, m Message) error {
	if err := ValidURL(c.URL); err != nil {
		return &sendError{code: "notify.invalid_url", err: err}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, strings.NewReader(m.Title))
	if err != nil {
		return err
	}
	priority, tag := "default", "warning"
	switch {
	case m.Resolved:
		priority, tag = "low", "white_check_mark"
	case m.Level == Crit:
		priority, tag = "high", "rotating_light"
	case m.Level == "info":
		tag = "information_source" // default priority: a test must ring like an alert
	}
	// HTTP headers are ASCII: RFC 2047 encoding for the "·" (ntfy decodes it).
	req.Header.Set("Title", mime.BEncoding.Encode("utf-8", "CrossWax NasBuster · "+m.NAS))
	req.Header.Set("Priority", priority)
	req.Header.Set("Tags", tag)
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	return n.do(req)
}

func (n *Notifier) webhook(ctx context.Context, c WebhookConfig, m Message) error {
	if err := ValidURL(c.URL); err != nil {
		return &sendError{code: "notify.invalid_url", err: err}
	}
	body, _ := json.Marshal(m)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return n.do(req)
}

func (n *Notifier) do(req *http.Request) error {
	req.Header.Set("User-Agent", "CrossWax-NasBuster")
	resp, err := n.Client.Do(req)
	if err != nil {
		// Never echo the full URL back: a topic name can be a secret.
		code := "notify.unreachable"
		if errors.Is(err, context.DeadlineExceeded) {
			code = "notify.timeout"
		}
		var ue *url.Error
		if errors.As(err, &ue) {
			return &sendError{code: code, err: fmt.Errorf("%s: %v", req.URL.Host, ue.Err)}
		}
		return &sendError{code: code, err: err}
	}
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &sendError{code: "notify.http_status", status: resp.StatusCode, err: fmt.Errorf("%s answered %d", req.URL.Host, resp.StatusCode)}
	}
	return nil
}
