// Package api is the public, goroutine-safe face of the idle-timeout watermark.
package api

import (
	"errors"
	"math"

	"ontology/idle"
)

// Distinct, decidable sentinel errors. Every rejected call changes no state.
var (
	ErrInvalidParams = errors.New("idle watermark: delay and timeout must be positive")
	ErrEmptyKey      = errors.New("idle watermark: event key must not be empty")
	ErrNegativeValue = errors.New("idle watermark: ts and pt must be non-negative")
)

// Flow is one idle-timeout watermark stream.
type Flow struct{ st *idle.Stream }

// New creates a Flow; delay and timeout must be positive.
func New(delay, timeout int64) (*Flow, error) {
	if delay <= 0 || timeout <= 0 {
		return nil, ErrInvalidParams
	}
	return &Flow{st: idle.New(delay, timeout)}, nil
}

// Feed delivers event {key, ts} at processing time pt.
func (f *Flow) Feed(key string, ts, pt int64) error {
	if key == "" { // validate fully before touching state -> no trace on reject
		return ErrEmptyKey
	}
	if ts < 0 || pt < 0 {
		return ErrNegativeValue
	}
	f.st.Feed(key, ts, pt)
	return nil
}

// Tick advances the processing-time clock without an event.
func (f *Flow) Tick(pt int64) error {
	if pt < 0 {
		return ErrNegativeValue
	}
	f.st.Tick(pt)
	return nil
}

// Watermark is the current watermark (math.MinInt64 before any contribution).
func (f *Flow) Watermark() int64 { return f.st.Watermark() }

// Dropped is the number of late events discarded.
func (f *Flow) Dropped() int { return f.st.Dropped() }

type op struct {
	feed   bool
	key    string
	ts, pt int64
}

// eightOps: NOTES built-in sequence (delay=3, timeout=5).
var eightOps = []op{
	{true, "a", 10, 0}, {true, "b", 20, 1}, {false, "", 0, 10},
	{true, "c", 20, 11}, {false, "", 0, 12}, {false, "", 0, 30},
	{true, "d", 25, 31}, {false, "", 0, 40},
}

// replay independently applies the rules, returning final wm, dropped
// count and every applied contribution; max(contrib) is the batch result.
func replay(delay, timeout int64, ops []op) (wm int64, dropped int, contrib []int64) {
	wm, lastPT := int64(math.MinInt64), int64(math.MinInt64)
	for _, o := range ops {
		if o.feed {
			lastPT = o.pt
			if m := o.ts - delay; m < wm { // strict: equal is accepted
				dropped++
			} else {
				contrib = append(contrib, m)
				wm = max(wm, m)
			}
			continue
		}
		if lastPT != math.MinInt64 && o.pt-lastPT < timeout {
			continue
		}
		m := o.pt - delay // processing-time advancement
		contrib = append(contrib, m)
		wm = max(wm, m)
	}
	return wm, dropped, contrib
}

// SelfCheck replays a built-in sequence on a private instance and
// verifies the four invariants; it never touches f's state.
func (f *Flow) SelfCheck() error {
	c, err := New(3, 5)
	if err != nil {
		return err
	}
	want, wantDrop, contrib := replay(3, 5, eightOps)
	prev, mono := c.Watermark(), true
	for i, o := range eightOps {
		if o.feed {
			err = c.Feed(o.key, o.ts, o.pt)
		} else {
			err = c.Tick(o.pt)
		}
		if err != nil {
			return err
		}
		mono = mono && c.Watermark() >= prev
		prev = c.Watermark()
		if i == 5 && c.Watermark() != 27 { // step 6: only pt-delay reaches 27
			return errors.New("selfcheck: idle not driven by processing time")
		}
	}
	batch := int64(math.MinInt64)
	for _, m := range contrib {
		batch = max(batch, m)
	}
	if batch != want || c.Watermark() != want || c.Dropped() != wantDrop || !mono {
		return errors.New("selfcheck: result/monotonicity mismatch")
	}

	// One event (mark 7), then an idle tick to pt=11 must push to 8.
	g, _ := New(3, 5)
	if err := g.Feed("a", 10, 0); err != nil {
		return err
	}
	if err := g.Tick(11); err != nil || g.Watermark() != 8 {
		return errors.New("selfcheck: processing-time idle push wrong")
	}
	// Rejections surface distinct sentinels and leave no trace; use resumes.
	r, _ := New(3, 5)
	rw, rd := r.Watermark(), r.Dropped()
	if !errors.Is(r.Feed("", 1, 0), ErrEmptyKey) ||
		!errors.Is(r.Feed("x", -1, 0), ErrNegativeValue) ||
		!errors.Is(r.Feed("x", 1, -1), ErrNegativeValue) ||
		!errors.Is(r.Tick(-1), ErrNegativeValue) {
		return errors.New("selfcheck: wrong sentinel error")
	}
	if r.Watermark() != rw || r.Dropped() != rd {
		return errors.New("selfcheck: rejected call changed state")
	}
	if err := r.Feed("ok", 10, 0); err != nil || r.Watermark() != 7 {
		return errors.New("selfcheck: stream unusable after rejection")
	}
	return nil
}
