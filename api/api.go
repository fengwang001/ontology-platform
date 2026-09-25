// Package api is the public face of the batch event-time aligner.
package api

import (
	"errors"
	"fmt"
	"slices"
	"sync"

	"ontology/aln"
	"ontology/lww"
)

// Distinguishable sentinel errors, one per rejectable failure.
var (
	ErrNonPositiveMax = errors.New("api: maxBatchEvents must be positive")
	ErrEmptyKey       = errors.New("api: event key must not be empty")
	ErrNoOpenBatch    = errors.New("api: no open batch")
	ErrBatchFull      = errors.New("api: batch accepted-event capacity exceeded")
)

// Ev is one upstream change event.
type Ev struct {
	Key string
	TS  int64
	V   int
}

// V is a batch event-time aligner. It is safe for concurrent use.
type V interface {
	BeginBatch()
	Feed(ev Ev) error
	EndBatch()
	Flush()
	Value(key string) (int, bool)
	AlignedTimes() []int64
	Dropped() int
	SelfCheck() error
}

type impl struct {
	mu  sync.RWMutex
	max int
	al  *aln.Aligner
	st  *lww.Store
}

// New creates an aligner holding at most maxBatchEvents accepted events per batch.
func New(maxBatchEvents int) (V, error) {
	if maxBatchEvents <= 0 {
		return nil, ErrNonPositiveMax
	}
	al := &aln.Aligner{}
	return &impl{max: maxBatchEvents, al: al, st: lww.New(al)}, nil
}

func (x *impl) BeginBatch() { x.mu.Lock(); defer x.mu.Unlock(); x.al.Begin() }
func (x *impl) EndBatch()   { x.mu.Lock(); defer x.mu.Unlock(); x.al.Close() }
func (x *impl) Flush()      { x.EndBatch() }

// Feed validates everything before mutating anything (invariant 4).
func (x *impl) Feed(ev Ev) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	switch {
	case !x.al.Open():
		return ErrNoOpenBatch
	case ev.Key == "":
		return ErrEmptyKey
	case !x.al.Late(ev.TS) && x.al.Count()+1 > x.max:
		return ErrBatchFull
	}
	x.st.Feed(ev.Key, ev.TS, ev.V)
	return nil
}

func (x *impl) Value(k string) (int, bool) { x.mu.RLock(); defer x.mu.RUnlock(); return x.st.Value(k) }
func (x *impl) AlignedTimes() []int64      { x.mu.RLock(); defer x.mu.RUnlock(); return x.al.Times() }
func (x *impl) Dropped() int               { x.mu.RLock(); defer x.mu.RUnlock(); return x.st.Dropped() }

// SelfCheck verifies the four invariants on fresh instances.
func (x *impl) SelfCheck() error { return selfcheck() }

func selfcheck() error {
	v, _ := New(5)
	var s uint64 = 42
	rnd := func(n int64) int64 { s = s*6364136223846793005 + 1442695040888963407; return int64(s>>33) % n }
	acc, mins, dropped, prev, hasPrev := map[string][]Ev{}, []int64{}, 0, aln.None, false
	for b := 0; b < 6; b++ {
		v.BeginBatch()
		cur, nAcc := aln.None, 0
		for i, n := int64(0), 1+rnd(5); i < n; i++ {
			e := Ev{Key: string(rune('a' + rnd(3))), TS: rnd(40), V: int(rnd(9))}
			if err := v.Feed(e); err != nil {
				return fmt.Errorf("selfcheck: feed: %w", err)
			}
			if hasPrev && e.TS < prev {
				dropped++
				continue
			}
			acc[e.Key] = append(acc[e.Key], e)
			cur, nAcc = min(cur, e.TS), nAcc+1
		}
		v.EndBatch()
		if nAcc > 0 {
			prev, hasPrev = cur, true
		}
		mins = append(mins, prev)
	}
	v.Flush()
	if got := v.AlignedTimes(); !slices.Equal(got, mins) { // invariant 2
		return fmt.Errorf("selfcheck: aligned %v want %v", got, mins)
	}
	if v.Dropped() != dropped { // invariant 3
		return fmt.Errorf("selfcheck: dropped %d want %d", v.Dropped(), dropped)
	}
	for k, evs := range acc { // invariant 1: max TS wins, tie later arrival
		best := evs[0]
		for _, e := range evs[1:] {
			if e.TS >= best.TS {
				best = e
			}
		}
		if val, ok := v.Value(k); !ok || val != best.V {
			return fmt.Errorf("selfcheck: value %q = %d,%v want %d", k, val, ok, best.V)
		}
	}
	return checkRejectNoTrace()
}

// checkRejectNoTrace verifies invariant 4: rejected operations change nothing.
func checkRejectNoTrace() error {
	_, e0 := New(0)
	v, _ := New(1)
	e1 := v.Feed(Ev{Key: "x", TS: 1}) // no open batch
	v.BeginBatch()
	e2 := v.Feed(Ev{TS: 1})                  // empty key
	ok := v.Feed(Ev{Key: "x", TS: 9, V: 1})  // valid
	e3 := v.Feed(Ev{Key: "x", TS: 10, V: 2}) // batch full
	v.Flush()
	val, has := v.Value("x")
	switch {
	case !errors.Is(e0, ErrNonPositiveMax), !errors.Is(e1, ErrNoOpenBatch),
		!errors.Is(e2, ErrEmptyKey), !errors.Is(e3, ErrBatchFull):
		return errors.New("selfcheck: rejection not distinguishable")
	case ok != nil, !has || val != 1, v.Dropped() != 0, !slices.Equal(v.AlignedTimes(), []int64{9}):
		return errors.New("selfcheck: rejected op left a trace")
	}
	return nil
}
