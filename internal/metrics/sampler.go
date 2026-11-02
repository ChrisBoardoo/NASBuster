// SPDX-License-Identifier: AGPL-3.0-or-later

// Package metrics samples CPU, memory, temperature and load into a fixed-size
// in-memory ring and fans samples out to live subscribers (SSE). No database,
// no collectd/rrdtool: the cost is a few file reads every interval.
package metrics

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/crosswax/nasbuster/internal/system"
)

// Sample is one measurement (API: /system/metrics and the SSE stream).
type Sample struct {
	TS            int64    `json:"ts"`
	CPUPercent    float64  `json:"cpuPercent"`
	MemUsedBytes  uint64   `json:"memUsedBytes"`
	MemTotalBytes uint64   `json:"memTotalBytes"`
	TempCelsius   *float64 `json:"tempCelsius"`
	Load1         float64  `json:"load1"`
}

// Sampler collects samples periodically.
type Sampler struct {
	Prober   *system.Prober
	Interval time.Duration
	Size     int // ring capacity

	mu      sync.Mutex
	ring    []Sample
	next    int
	full    bool
	prevCPU system.CPUTimes
	hasPrev bool
	subs    map[chan Sample]struct{}
	started bool
}

// New returns a sampler keeping 15 minutes at a 2-second interval.
func New(p *system.Prober) *Sampler {
	return &Sampler{Prober: p, Interval: 2 * time.Second, Size: 450}
}

// Run samples until ctx is done.
func (s *Sampler) Run(ctx context.Context) {
	s.mu.Lock()
	if s.ring == nil {
		s.ring = make([]Sample, s.Size)
	}
	s.started = true
	s.mu.Unlock()

	s.Tick(time.Now())
	t := time.NewTicker(s.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			s.Tick(now)
		}
	}
}

// Tick takes one sample now. Exported for tests.
func (s *Sampler) Tick(now time.Time) Sample {
	sample := Sample{TS: now.Unix()}
	cpu, _, cpuErr := s.Prober.CPUTimes()
	if total, avail, err := s.Prober.MemInfo(); err == nil {
		sample.MemTotalBytes = total
		if avail <= total {
			sample.MemUsedBytes = total - avail
		}
	}
	if c, ok := s.Prober.Temperature(); ok {
		c = math.Round(c*10) / 10
		sample.TempCelsius = &c
	}
	if l, ok := s.Prober.Load1(); ok {
		sample.Load1 = l
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ring == nil {
		s.ring = make([]Sample, s.Size)
	}
	if cpuErr == nil {
		if s.hasPrev && cpu.Total > s.prevCPU.Total {
			dTotal := float64(cpu.Total - s.prevCPU.Total)
			dIdle := float64(cpu.Idle - s.prevCPU.Idle)
			sample.CPUPercent = math.Round((1-dIdle/dTotal)*1000) / 10
			sample.CPUPercent = math.Max(0, math.Min(100, sample.CPUPercent))
		}
		s.prevCPU, s.hasPrev = cpu, true
	}
	s.ring[s.next] = sample
	s.next = (s.next + 1) % len(s.ring)
	if s.next == 0 {
		s.full = true
	}
	for ch := range s.subs {
		select {
		case ch <- sample:
		default: // slow subscriber: drop rather than block sampling
		}
	}
	return sample
}

// History returns samples oldest first.
func (s *Sampler) History() []Sample {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ring == nil {
		return []Sample{}
	}
	var out []Sample
	if s.full {
		out = append(out, s.ring[s.next:]...)
	}
	out = append(out, s.ring[:s.next]...)
	if out == nil {
		out = []Sample{}
	}
	return out
}

// Current returns the latest sample.
func (s *Sampler) Current() (Sample, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ring == nil || (!s.full && s.next == 0) {
		return Sample{}, false
	}
	return s.ring[(s.next-1+len(s.ring))%len(s.ring)], true
}

// Subscribe returns a channel receiving every new sample, and a cancel func.
func (s *Sampler) Subscribe() (<-chan Sample, func()) {
	ch := make(chan Sample, 4)
	s.mu.Lock()
	if s.subs == nil {
		s.subs = map[chan Sample]struct{}{}
	}
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	return ch, func() {
		s.mu.Lock()
		delete(s.subs, ch)
		s.mu.Unlock()
	}
}
