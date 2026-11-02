// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/crosswax/nasbuster/internal/store"
)

func TestJournal(t *testing.T) {
	var st *store.Store
	e := newEnvWith(t, func(d *Deps) { st = d.Store })
	e.setup()
	ctx := context.Background()
	for _, ev := range []store.Event{
		{Time: 100, TimeState: store.TimeExact, Level: "info", Code: "system.boot", Subject: "system"},
		{Time: 200, TimeState: store.TimeUncertain, Level: "crit", Code: "pool.degraded", Phase: "raised", Subject: "pool:e1", Params: map[string]any{"pool": "e1"}},
		{Time: 300, TimeState: store.TimeExact, Level: "info", Code: "pool.degraded", Phase: "resolved", Subject: "pool:e1", Params: map[string]any{"pool": "e1"}},
	} {
		if _, err := st.AddEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}

	var page struct {
		Events []store.Event
		Next   int64
	}
	e.do("GET", "/api/v1/journal", nil, http.StatusOK, &page)
	if len(page.Events) != 3 || page.Events[0].Time != 300 || page.Next != 0 || page.Events[1].TimeState != store.TimeUncertain {
		t.Fatalf("journal = %+v", page)
	}
	e.do("GET", "/api/v1/journal?level=warn", nil, http.StatusOK, &page)
	if len(page.Events) != 1 || page.Events[0].Code != "pool.degraded" || page.Events[0].Params["pool"] != "e1" {
		t.Fatalf("warn and worse = %+v", page)
	}
	e.do("GET", "/api/v1/journal?subject=pool:&limit=1", nil, http.StatusOK, &page)
	if len(page.Events) != 1 || page.Events[0].Phase != "resolved" || page.Next == 0 {
		t.Fatalf("first page = %+v", page)
	}
	next := strconv.FormatInt(page.Next, 10)
	e.do("GET", "/api/v1/journal?subject=pool:&limit=1&before="+next, nil, http.StatusOK, &page)
	if len(page.Events) != 1 || page.Events[0].Phase != "raised" {
		t.Fatalf("second page = %+v", page)
	}

	for query, code := range map[string]string{
		"level=loud":        "validation.level",
		"limit=0":           "validation.limit",
		"limit=501":         "validation.limit",
		"before=x":          "validation.before",
		"subject=../etc":    "validation.subject",
		"subject=pool:A%25": "validation.subject",
	} {
		if c := errCode(t, e, "GET", "/api/v1/journal?"+query, nil, http.StatusBadRequest); c != code {
			t.Errorf("%s: %s, want %s", query, c, code)
		}
	}

	e.do("POST", "/api/v1/auth/logout", nil, http.StatusNoContent, nil)
	if c := errCode(t, e, "GET", "/api/v1/journal", nil, http.StatusUnauthorized); c != "auth.required" {
		t.Fatalf("journal without a session: %s", c)
	}
}

// The system journal kept across reboots: on by default, off on request (ADR-0031).
func TestJournalSettings(t *testing.T) {
	e := newEnv(t)
	e.setup()
	type view struct {
		Persistent, Active bool
		ManagedBy          string
	}
	var v view
	// Nothing applied yet (the daemon does it at start): on by default, not in place.
	e.do("GET", "/api/v1/journal/settings", nil, http.StatusOK, &v)
	if !v.Persistent || v.Active || v.ManagedBy != "" {
		t.Fatalf("defaults = %+v", v)
	}
	e.do("PUT", "/api/v1/journal/settings", map[string]any{"persistent": true}, http.StatusOK, &v)
	if !v.Persistent || !v.Active {
		t.Fatalf("on = %+v", v)
	}
	if b, err := e.sys.Files.ReadFile("/etc/systemd/journald.conf.d/60-cwnsb-persistent.conf"); err != nil || !strings.Contains(string(b), "Storage=persistent") {
		t.Fatalf("drop-in = %q %v", b, err)
	}
	e.do("PUT", "/api/v1/journal/settings", map[string]any{"persistent": false}, http.StatusOK, &v)
	e.do("GET", "/api/v1/journal/settings", nil, http.StatusOK, &v)
	if v.Persistent || v.Active || e.sys.Files.Exists("/etc/systemd/journald.conf.d/60-cwnsb-persistent.conf") {
		t.Fatalf("off = %+v", v)
	}
	var audit struct {
		Entries []struct{ Action, Detail string }
	}
	e.do("GET", "/api/v1/audit?limit=2", nil, http.StatusOK, &audit)
	if audit.Entries[0].Action != "journal.persistent" || audit.Entries[0].Detail != "off" || audit.Entries[1].Detail != "on" {
		t.Fatalf("audit = %+v", audit.Entries)
	}
}
