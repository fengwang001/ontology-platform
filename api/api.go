// Package api is the public face of the materialized-view staleness detector.
package api

import (
	"errors"

	"ontology/stale"
)

// The four rejection causes are distinct, decidable sentinel errors.
var (
	ErrInvalidTimeout     = errors.New("api: timeout must be positive") // New with timeout <= 0
	ErrDataGap            = stale.ErrDataGap                            // Data Seq != applied+1
	ErrWatermarkBacktrack = stale.ErrWatermarkBacktrack                 // Watermark UpTo < W
	ErrTickBacktrack      = stale.ErrTickBacktrack                      // Tick t < now
)

// Record kinds are aliases: the public feed takes the same value types the
// stale package switches on.
type (
	Data      = stale.Data
	Watermark = stale.Watermark
	Heartbeat = stale.Heartbeat
)

// Service is a concurrent-safe, in-memory materialized view.
type Service struct {
	d *stale.Detector
}

// New builds a service with the fixed timeout constant. timeout <= 0 is
// rejected before any state exists.
func New(timeout int64) (*Service, error) {
	if timeout <= 0 {
		return nil, ErrInvalidTimeout
	}
	return &Service{d: stale.NewDetector(timeout)}, nil
}

// Feed applies one change-stream record.
func (s *Service) Feed(rec any) error { return s.d.Feed(rec) }

// Tick advances the logical clock.
func (s *Service) Tick(t int64) error { return s.d.Tick(t) }

// Stale reports (A < W) || (now-lastHB > timeout).
func (s *Service) Stale() bool { return s.d.Stale() }

// Applied returns the last applied Data sequence.
func (s *Service) Applied() int64 { return s.d.Applied() }

// Watermark returns the current watermark W.
func (s *Service) Watermark() int64 { return s.d.Watermark() }

// View returns the sum of Val over all applied Data records.
func (s *Service) View() int64 { return s.d.View() }

type checkStep struct {
	rec     any   // nil means this step is a Tick of tick
	tick    int64 // used when rec == nil
	a, w, n int64 // expected A, W, now
	hb      int64 // expected lastHB
	stale   bool  // expected stale
}

// eightStepScript is the fixed built-in sequence from NOTES.md section 3.
var eightStepScript = []checkStep{
	{rec: Data{Seq: 1, Val: 10}, a: 1, w: 0, n: 0, hb: 0, stale: false},
	{rec: Watermark{UpTo: 4}, a: 1, w: 4, n: 0, hb: 0, stale: true},
	{rec: Data{Seq: 2, Val: 20}, a: 2, w: 4, n: 0, hb: 0, stale: true},
	{rec: Data{Seq: 3, Val: 30}, a: 3, w: 4, n: 0, hb: 0, stale: true},
	{rec: Data{Seq: 4, Val: 40}, a: 4, w: 4, n: 0, hb: 0, stale: false},
	{tick: 5, a: 4, w: 4, n: 5, hb: 0, stale: false},
	{tick: 6, a: 4, w: 4, n: 6, hb: 0, stale: true},
	{rec: Heartbeat{}, a: 4, w: 4, n: 6, hb: 6, stale: false},
}

// SelfCheck replays the built-in sequence and verifies the four invariants:
// view-sum/progress, the exact stale formula at every step, monotonicity of
// W/lastHB/now, and failure-leaves-no-trace for the distinct rejections.
func (s *Service) SelfCheck() bool {
	c, err := New(5)
	if err != nil {
		return false
	}
	var pw, ph, pn, sum int64
	for _, q := range eightStepScript {
		if q.rec != nil {
			if e := c.Feed(q.rec); e != nil {
				return false
			}
			if d, ok := q.rec.(Data); ok {
				sum += d.Val
			}
		} else if e := c.Tick(q.tick); e != nil {
			return false
		}
		// Invariant 2: exact formula, moment by moment.
		got := c.Applied() < c.Watermark() || c.d.Now()-c.d.LastBeat() > 5
		if c.Applied() != q.a || c.Watermark() != q.w ||
			c.d.Now() != q.n || c.d.LastBeat() != q.hb || got != q.stale {
			return false
		}
		// Invariant 3: W, lastHB, now never decrease.
		if c.Watermark() < pw || c.d.LastBeat() < ph || c.d.Now() < pn {
			return false
		}
		pw, ph, pn = c.Watermark(), c.d.LastBeat(), c.d.Now()
	}
	// Invariant 1: view equals the sum of applied Vals; invariant 4 follows.
	if c.View() != sum || sum != 100 || c.Applied() != 4 {
		return false
	}
	return selfCheckRejections(c)
}

// selfCheckRejections verifies state is unchanged after each rejection.
func selfCheckRejections(c *Service) bool {
	snapshot := func() [6]int64 {
		return [6]int64{c.Applied(), c.Watermark(), c.d.Now(), c.d.LastBeat(), c.View(), b2i(c.Stale())}
	}
	cases := []struct {
		want error
		act  func() error
	}{
		{ErrDataGap, func() error { return c.Feed(Data{Seq: 99, Val: 1}) }},
		{ErrWatermarkBacktrack, func() error { return c.Feed(Watermark{UpTo: 3}) }},
		{ErrTickBacktrack, func() error { return c.Tick(1) }},
	}
	for _, tc := range cases {
		before := snapshot()
		err := tc.act()
		if !errors.Is(err, tc.want) || snapshot() != before {
			return false
		}
	}
	if _, err := New(0); !errors.Is(err, ErrInvalidTimeout) {
		return false
	}
	return true
}

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
