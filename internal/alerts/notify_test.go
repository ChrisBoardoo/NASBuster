// SPDX-License-Identifier: AGPL-3.0-or-later

package alerts

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidURL(t *testing.T) {
	for _, ok := range []string{"https://ntfy.sh/my-nas-1234", "http://192.168.1.10:8080/nas", "http://ntfy.local/x", "http://127.0.0.1:9/x"} {
		if err := ValidURL(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "ntfy.sh/topic", "ftp://x/y", "http://ntfy.sh/topic", "https://user:pass@ntfy.sh/x", "javascript:alert(1)", "https://" + strings.Repeat("a", 600)} {
		if err := ValidURL(bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestSendNtfyAndWebhook(t *testing.T) {
	var ntfyReq *http.Request
	var ntfyBody, hookBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if r.URL.Path == "/topic" {
			ntfyReq, ntfyBody = r, b
		} else {
			hookBody = b
		}
	}))
	defer srv.Close()
	ch := Channels{
		Ntfy:    NtfyConfig{Enabled: true, URL: srv.URL + "/topic", Token: "tk_secret"},
		Webhook: WebhookConfig{Enabled: true, URL: srv.URL + "/hook"},
	}
	m := Message{NAS: "nasbuster", Title: "Pool tank is degraded", Level: Crit, Code: "pool.degraded", Params: map[string]any{"pool": "tank"}}
	res := NewNotifier().Send(context.Background(), ch, m)
	if len(res) != 2 || !res[0].OK || !res[1].OK {
		t.Fatalf("results = %+v", res)
	}
	if string(ntfyBody) != "Pool tank is degraded" || ntfyReq.Header.Get("Priority") != "high" ||
		ntfyReq.Header.Get("Authorization") != "Bearer tk_secret" || decodeHeader(ntfyReq.Header.Get("Title")) != "CrossWax NasBuster · nasbuster" {
		t.Fatalf("ntfy = %q %v", ntfyBody, ntfyReq.Header)
	}
	var got Message
	if err := json.Unmarshal(hookBody, &got); err != nil || got.Code != "pool.degraded" || got.Params["pool"] != "tank" {
		t.Fatalf("webhook = %s %v", hookBody, err)
	}
}

func TestNoRedirectsAndErrors(t *testing.T) {
	redirect := httptest.NewServer(http.RedirectHandler("https://evil.example/steal", http.StatusFound))
	defer redirect.Close()
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer failing.Close()
	n := NewNotifier()
	for _, u := range []string{redirect.URL, failing.URL} {
		res := n.Send(context.Background(), Channels{Webhook: WebhookConfig{Enabled: true, URL: u}}, Message{})
		if len(res) != 1 || res[0].OK || strings.Contains(res[0].Error, "/steal") {
			t.Errorf("%s: %+v", u, res)
		}
	}
	if res := n.Send(context.Background(), Channels{}, Message{}); len(res) != 0 {
		t.Fatal("disabled channels must not send")
	}
}

func decodeHeader(v string) string {
	out, err := new(mime.WordDecoder).DecodeHeader(v)
	if err != nil {
		return "undecodable: " + v
	}
	return out
}

func TestTitleHeaderIsASCII(t *testing.T) {
	var title string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { title = r.Header.Get("Title") }))
	defer srv.Close()
	NewNotifier().Send(context.Background(), Channels{Ntfy: NtfyConfig{Enabled: true, URL: srv.URL + "/t"}},
		Message{NAS: "nas", Title: "Résolu : test", Level: "info", Code: "test"})
	for _, r := range title {
		if r > 127 {
			t.Fatalf("non-ASCII header: %q", title)
		}
	}
}

func TestFailuresAreClassified(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer srv.Close()
	res := NewNotifier().Send(context.Background(), Channels{Webhook: WebhookConfig{Enabled: true, URL: srv.URL + "/h"}}, Message{})
	if len(res) != 1 || res[0].Code != "notify.http_status" || res[0].Status != 403 {
		t.Fatalf("403 = %+v", res)
	}
	srv.Close()
	res = NewNotifier().Send(context.Background(), Channels{Webhook: WebhookConfig{Enabled: true, URL: srv.URL + "/h"}}, Message{})
	if len(res) != 1 || res[0].Code != "notify.unreachable" {
		t.Fatalf("closed = %+v", res)
	}
	if !IsLocalURL("http://192.168.1.10/x") || IsLocalURL("https://ntfy.sh/x") {
		t.Fatal("IsLocalURL")
	}
}
