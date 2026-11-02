// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"testing"
)

func TestEventsFilterPageAndPrune(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	add := func(ts int64, level, subject string) {
		if _, err := s.AddEvent(ctx, Event{Time: ts, TimeState: TimeExact, Level: level, Code: "c", Subject: subject}); err != nil {
			t.Fatal(err)
		}
	}
	add(100, "info", "system")
	add(200, "crit", "pool:e1")
	add(300, "warn", "pool:tank")
	add(400, "error", "disk:usb-X-0:0")

	all, err := s.Events(ctx, EventFilter{})
	if err != nil || len(all) != 4 || all[0].Time != 400 || all[0].Params == nil {
		t.Fatalf("all = %+v %v", all, err)
	}
	if got, _ := s.Events(ctx, EventFilter{MinLevel: "error"}); len(got) != 2 {
		t.Fatalf("error and worse = %+v", got)
	}
	if got, _ := s.Events(ctx, EventFilter{Subject: "pool:"}); len(got) != 2 {
		t.Fatalf("pools = %+v", got)
	}
	if got, _ := s.Events(ctx, EventFilter{Subject: "pool:e1"}); len(got) != 1 || got[0].Level != "crit" {
		t.Fatalf("pool e1 = %+v", got)
	}
	if got, _ := s.Events(ctx, EventFilter{Before: all[1].ID, Limit: 1}); len(got) != 1 || got[0].Time != 200 {
		t.Fatalf("page = %+v", got)
	}
	if _, err := s.Events(ctx, EventFilter{MinLevel: "loud"}); !errors.Is(err, ErrInvalidData) {
		t.Fatalf("bad level: %v", err)
	}
	// Keep the 2 newest, none before 150.
	if err := s.PruneEvents(ctx, 2, 150); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Events(ctx, EventFilter{}); len(got) != 2 || got[1].Time != 300 {
		t.Fatalf("after prune = %+v", got)
	}
}
