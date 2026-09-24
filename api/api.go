// Package api is the public entry point of the adaptive watermark; in-process memory, stdlib only.
package api

import (
	"errors"
	"fmt"
	"math/rand"

	"ontology/adapt"
)

// ErrSelfCheck wraps every failure reported by SelfCheck.
var ErrSelfCheck = errors.New("api: self-check failed")

// Store is an adaptive watermark instance.
type Store struct{ w *adapt.Window }

// New validates every parameter before constructing anything; on rejection it
// returns (nil, sentinel error) and creates no state.
func New(minDelay, maxDelay, step, width, hi, lo int64) (*Store, error) {
	w, err := adapt.New(minDelay, maxDelay, step, width, hi, lo)
	if err != nil {
		return nil, err
	}
	return &Store{w: w}, nil
}

// Feed applies one event and reports whether it was late.
func (s *Store) Feed(ts int64) bool { return s.w.Feed(ts) }

// WM returns the current watermark.
func (s *Store) WM() int64 { return s.w.WM() }

// Delay returns the current delay, always within [minDelay, maxDelay].
func (s *Store) Delay() int64 { return s.w.Delay() }

// nineSteps (min=2 max=10 step=2 W=3 hi=2 lo=0): {ts,late,wm,delay after}.
var nineSteps = []struct {
	ts        int64
	late      bool
	wm, delay int64
}{
	{10, false, 8, 2}, {11, false, 9, 2}, {9, false, 9, 2},
	{5, true, 9, 2}, {6, true, 9, 2}, {15, false, 13, 4},
	{16, false, 12, 4}, {17, false, 13, 4}, {18, false, 14, 2},
}

// SelfCheck replays built-in sequences and verifies the four invariants.
func (s *Store) SelfCheck() error {
	st, err := New(2, 10, 2, 3, 2, 0)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSelfCheck, err)
	}
	for i, want := range nineSteps { // worked example: boundary, up and down
		if st.Feed(want.ts) != want.late || st.WM() != want.wm || st.Delay() != want.delay {
			return fmt.Errorf("%w: step %d", ErrSelfCheck, i+1)
		}
	}
	if err := checkFixedDelay(); err != nil {
		return err
	}
	if err := checkConvergence(); err != nil {
		return err
	}
	if err := adapt.ConstantSettlementCost(); err != nil {
		return fmt.Errorf("%w: settlement cost: %v", ErrSelfCheck, err)
	}
	return checkRejectThenUse(st)
}

// checkFixedDelay verifies invariant 1 against a naive model on neutral
// windows, then invariant 2 bounds under random arrivals.
func checkFixedDelay() error {
	events := []int64{10, 7, 11, 12, 13, 10, 14, 15, 16, 13, 17, 18}
	st, _ := New(2, 10, 2, 4, 4, 0)
	var maxSeen int64
	for i, ts := range events {
		naive := i > 0 && ts < maxSeen-2
		if st.Feed(ts) != naive || st.Delay() != 2 {
			return fmt.Errorf("%w: invariant1 at %d", ErrSelfCheck, i)
		}
		if ts > maxSeen {
			maxSeen = ts
		}
	}
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 300; i++ {
		st.Feed(r.Int63n(1000) - 500)
		if d := st.Delay(); d < 2 || d > 10 {
			return fmt.Errorf("%w: invariant2 delay %d", ErrSelfCheck, d)
		}
	}
	return nil
}

// checkConvergence verifies invariant 3: late-heavy windows walk the delay up
// one step each to maxDelay and hold there; on-time windows then walk it back
// down one step each to minDelay and hold there.
func checkConvergence() error {
	st, _ := New(2, 10, 2, 3, 2, 0)
	cases := []struct {
		f    [3]int64
		want int64
	}{
		{[3]int64{100, 1, 2}, 4},
		{[3]int64{1, 2, 3}, 6}, {[3]int64{1, 2, 3}, 8},
		{[3]int64{1, 2, 3}, 10}, {[3]int64{1, 2, 3}, 10},
		{[3]int64{2000, 2001, 2002}, 8}, {[3]int64{2003, 2004, 2005}, 6},
		{[3]int64{2006, 2007, 2008}, 4}, {[3]int64{2009, 2010, 2011}, 2},
		{[3]int64{2012, 2013, 2014}, 2},
	}
	for i, c := range cases {
		for _, ts := range c.f {
			st.Feed(ts)
		}
		if st.Delay() != c.want {
			return fmt.Errorf("%w: invariant3 %d got %d", ErrSelfCheck, i, st.Delay())
		}
	}
	return nil
}

// checkRejectThenUse verifies invariant 4: five distinct sentinel rejections,
// and an existing store stays usable after rejected constructions.
func checkRejectThenUse(st *Store) error {
	bad := []struct {
		args [6]int64
		want error
	}{
		{[6]int64{9, 8, 1, 3, 2, 0}, adapt.ErrMinAboveMax},
		{[6]int64{-1, 8, 1, 3, 2, 0}, adapt.ErrNegativeMin},
		{[6]int64{2, 8, 0, 3, 2, 0}, adapt.ErrNonPositiveStep},
		{[6]int64{2, 8, 1, 0, 2, 0}, adapt.ErrNonPositiveWindow},
		{[6]int64{2, 8, 1, 3, 2, 2}, adapt.ErrThresholdRange},
	}
	for i, b := range bad {
		got, err := New(b.args[0], b.args[1], b.args[2], b.args[3], b.args[4], b.args[5])
		if got != nil || !errors.Is(err, b.want) {
			return fmt.Errorf("%w: rejection %d got (%v,%v)", ErrSelfCheck, i, got, err)
		}
	}
	if _, err := New(-1, 1, 1, 1, 1, 0); !errors.Is(err, adapt.ErrNegativeMin) {
		return fmt.Errorf("%w: rejection disturbed caller", ErrSelfCheck)
	}
	st.Feed(100) // the worked-example store must still work afterwards
	if st.WM() != 98 || st.Delay() != 2 {
		return fmt.Errorf("%w: store unusable wm=%d", ErrSelfCheck, st.WM())
	}
	return nil
}
