// SPDX-License-Identifier: AGPL-3.0-or-later

package metrics

import (
	"testing"
	"time"

	"github.com/crosswax/nasbuster/internal/demo"
	"github.com/crosswax/nasbuster/internal/system"
)

func TestSamplerRingAndSubscribers(t *testing.T) {
	sys := demo.New()
	clock := time.Now()
	sys.Clock = func() time.Time { return clock } // deterministic demo counters
	s := &Sampler{Prober: system.New(sys.FS, sys.Runner), Interval: time.Second, Size: 3}
	ch, cancel := s.Subscribe()
	defer cancel()

	start := time.Unix(1_790_000_000, 0)
	first := s.Tick(start)
	if first.CPUPercent != 0 || first.MemTotalBytes != 7997684*1024 || first.TempCelsius == nil {
		t.Fatalf("first sample = %+v", first)
	}
	for i := 1; i <= 4; i++ {
		clock = clock.Add(2 * time.Second)
		s.Tick(start.Add(time.Duration(i) * time.Second))
	}
	h := s.History()
	if len(h) != 3 || h[0].TS != start.Unix()+2 || h[2].TS != start.Unix()+4 {
		t.Fatalf("history = %+v", h)
	}
	cur, ok := s.Current()
	if !ok || cur.TS != start.Unix()+4 {
		t.Fatalf("current = %+v", cur)
	}
	if cur.CPUPercent <= 0 || cur.CPUPercent > 100 {
		t.Errorf("cpu = %v, want (0,100]", cur.CPUPercent)
	}
	received := 0
	for len(ch) > 0 {
		<-ch
		received++
	}
	if received != 4 { // buffer of 4, fifth sample dropped (slow subscriber)
		t.Errorf("received %d samples, want 4", received)
	}
}

func TestEmptySampler(t *testing.T) {
	s := New(nil)
	if _, ok := s.Current(); ok {
		t.Fatal("empty sampler has a current sample")
	}
	if h := s.History(); h == nil || len(h) != 0 {
		t.Fatalf("history = %v", h)
	}
}
