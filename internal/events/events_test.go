// SPDX-License-Identifier: AGPL-3.0-or-later

package events

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/crosswax/nasbuster/internal/store"
)

type env struct {
	st     *store.Store
	rec    *Recorder
	now    time.Time
	synced bool
	bootID string
	uptime time.Duration
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	e := &env{st: st, now: time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC), synced: true, bootID: "boot-1", uptime: time.Minute}
	e.rec = &Recorder{Store: st, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:    func() time.Time { return e.now },
		Synced: func() (bool, bool) { return e.synced, true },
		Boot:   func() (string, time.Duration, bool) { return e.bootID, e.uptime, true },
	}
	return e
}

func (e *env) events(t *testing.T) []store.Event {
	t.Helper()
	list, err := e.st.Events(context.Background(), store.EventFilter{})
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func codes(list []store.Event) []string {
	var out []string
	for i := len(list) - 1; i >= 0; i-- { // oldest first
		out = append(out, list[i].Code)
	}
	return out
}

// A power cut, no network at boot: the clock restarts from the last saved
// time (the 18th) until NTP sets it (the 25th).
func TestUncertainTimeIsRecomputed(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	real := e.now
	e.synced, e.uptime, e.now = false, 10*time.Second, time.Date(2026, 10, 18, 5, 0, 0, 0, time.UTC)
	e.rec.Record(ctx, store.Event{Level: "crit", Code: "pool.degraded", Phase: "raised", Subject: "pool:e1"})
	if got := e.events(t)[0]; got.TimeState != store.TimeUncertain || got.Time != e.now.Unix() || got.BootID != "boot-1" {
		t.Fatalf("before the clock sync = %+v", got)
	}
	// An uncertain event of an older boot cannot be corrected.
	e.bootID = "boot-0"
	e.rec.Record(ctx, store.Event{Level: "info", Code: "system.boot", Subject: "system"})

	// NTP sets the clock 70 s after boot: the event was 60 s before.
	e.bootID, e.synced, e.uptime, e.now = "boot-1", true, 70*time.Second, real
	e.rec.Sync(ctx)
	list := e.events(t)
	if got := list[1]; got.TimeState != store.TimeCorrected || got.Time != real.Add(-60*time.Second).Unix() {
		t.Fatalf("after the clock sync = %+v, want %d", got, real.Add(-60*time.Second).Unix())
	}
	if list[0].TimeState != store.TimeUncertain {
		t.Fatalf("another boot was corrected: %+v", list[0])
	}
	// Synchronized from now on: exact.
	e.rec.Record(ctx, store.Event{Level: "info", Code: "pool.degraded", Phase: "resolved", Subject: "pool:e1"})
	if got := e.events(t)[0]; got.TimeState != store.TimeExact || got.Time != real.Unix() {
		t.Fatalf("synchronized = %+v", got)
	}
}

func TestBootsAndUncleanShutdowns(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	e.rec.Start(ctx, 0) // first start ever, a minute after the boot
	if got := codes(e.events(t)); len(got) != 1 || got[0] != "system.boot" {
		t.Fatalf("first boot = %v", got)
	}
	if boot := e.events(t)[0]; boot.Time != e.now.Add(-e.uptime).Unix() || boot.UptimeMs != 0 {
		t.Fatalf("the boot is dated at the boot: %+v", boot)
	}
	// The daemon alone restarts (an update): same boot, nothing new.
	e.rec.Stop(ctx)
	e.rec.Start(ctx, 0)
	if got := codes(e.events(t)); len(got) != 1 {
		t.Fatalf("daemon restart = %v", got)
	}
	// A clean shutdown, then a new boot.
	e.rec.Stop(ctx)
	e.bootID = "boot-2"
	e.rec.Start(ctx, 0)
	if got := codes(e.events(t)); len(got) != 2 || got[1] != "system.boot" {
		t.Fatalf("clean reboot = %v", got)
	}
	// The power goes: no Stop. The next boot says so, with the last sign of life.
	lastAlive := e.now.Add(-3 * time.Hour).Unix()
	e.bootID = "boot-3"
	e.rec.Start(ctx, lastAlive)
	list := e.events(t)
	if got := codes(list); len(got) != 4 || got[2] != "system.unclean_shutdown" || got[3] != "system.boot" {
		t.Fatalf("power cut = %v", got)
	}
	if u := list[1]; u.Level != "error" || u.Params["lastAlive"] != float64(lastAlive) {
		t.Fatalf("unclean shutdown = %+v", u)
	}
}

func TestAlertTransitions(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.rec.AlertRaised(ctx, "crit", "pool.degraded", map[string]any{"pool": "e1"})
	e.rec.AlertRaised(ctx, "warn", "smart.crc", map[string]any{"disk": "usb-Lexar_USB_Flash_Drive_1-0:0", "increase": 3})
	e.rec.AlertRaised(ctx, "warn", "power.under_voltage_occurred", nil)
	e.rec.AlertResolved(ctx, store.Alert{Level: "crit", Code: "pool.degraded", Params: map[string]any{"pool": "e1"}})
	list := e.events(t)
	want := []struct{ subject, level, phase string }{
		{"pool:e1", "info", "resolved"},
		{"system", "warn", "raised"},
		{"disk:usb-Lexar_USB_Flash_Drive_1-0:0", "warn", "raised"},
		{"pool:e1", "crit", "raised"},
	}
	for i, w := range want {
		if got := list[i]; got.Subject != w.subject || got.Level != w.level || got.Phase != w.phase || got.Source != SourceAlert {
			t.Errorf("event %d = %+v, want %+v", i, got, w)
		}
	}
}

func TestNilRecorderRecordsNothing(t *testing.T) {
	var r *Recorder
	r.Record(context.Background(), store.Event{Code: "x"})
	r.Sync(context.Background())
	r.Start(context.Background(), 0)
	r.Stop(context.Background())
}
