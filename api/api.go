// Package api is the public entry point: a thread-safe unwrapping tracker.
// Dependency direction is one-way: api -> eng -> wrap.
package api

import (
	"errors"
	"fmt"
	"math"

	"ontology/eng"
	"ontology/wrap"
)

// Event re-exports the classification type.
type Event = eng.Event

const (
	First     = eng.First
	Forward   = eng.Forward
	Wrap      = eng.Wrap
	Duplicate = eng.Duplicate
)

// Sentinel errors. All four are distinct and decidable via errors.Is.
var (
	ErrInvalidThreshold = errors.New("api: threshold must satisfy 1 <= threshold < 2^31")
	ErrRegression       = eng.ErrRegression // r < prev with drop <= threshold
	ErrEmpty            = eng.ErrEmpty      // query before any accepted Feed
	ErrOverflow         = eng.ErrOverflow   // unwrapped value would exceed MaxInt64
)

// MaxThreshold is the exclusive upper bound for a legal threshold.
const MaxThreshold = uint32(1) << 31

// Tracker is the concurrency-safe public tracker.
type Tracker struct{ e *eng.Engine }

// New validates the threshold and builds a tracker.
func New(threshold uint32) (*Tracker, error) {
	if threshold < 1 || threshold >= MaxThreshold {
		return nil, ErrInvalidThreshold
	}
	return &Tracker{e: eng.New(threshold)}, nil
}

func (t *Tracker) Feed(r uint32) (Event, error)  { return t.e.Feed(r) }
func (t *Tracker) LastUnwrapped() (int64, error) { return t.e.LastUnwrapped() }
func (t *Tracker) LastRaw() (uint32, error)      { return t.e.LastRaw() }
func (t *Tracker) Counts() map[Event]int         { return t.e.Counts() }

// selfCheckSeq is the section-3 trace plus a trailing duplicate; index 5 (40)
// is the rejected regression.
var selfCheckSeq = []uint32{100, 200, 4294967200, 50, 60, 40, 4294967295, 5, 5}

// SelfCheck replays the built-in sequence and verifies the four invariants:
// agreement with an independent offline replay, monotonicity, the exact wrap
// formula, and that rejection leaves no trace; then checks every sentinel.
func (t *Tracker) SelfCheck() error {
	const th = MaxThreshold - 1
	var npv uint32
	var npu int64
	has := false
	want := make([]int64, len(selfCheckSeq))
	for i, r := range selfCheckSeq {
		switch {
		case !has:
			npv, npu, has = r, int64(r), true
		case r == npv:
		case r > npv:
			npu += int64(uint64(r) - uint64(npv))
			npv = r
		case uint64(npv)-uint64(r) > uint64(th):
			npu += int64(wrap.Size - uint64(npv) + uint64(r))
			npv = r
		}
		want[i] = npu
	}
	tr, _ := New(th)
	var last int64
	for i, r := range selfCheckSeq {
		u0, _ := tr.LastUnwrapped()
		r0, _ := tr.LastRaw()
		c0 := tr.Counts()
		ev, ferr := tr.Feed(r)
		u, _ := tr.LastUnwrapped()
		if u != want[i] { // invariant 1
			return fmt.Errorf("invariant1: step %d %d!=%d", i, u, want[i])
		}
		if u < last || (ferr == nil && ev != Duplicate && i > 0 && u <= last) { // invariant 2
			return fmt.Errorf("invariant2: step %d", i)
		}
		if ferr != nil { // invariant 4: no trace
			if rb, _ := tr.LastRaw(); u != u0 || rb != r0 || !eq(tr.Counts(), c0) {
				return fmt.Errorf("invariant4: step %d traced", i)
			}
		}
		if ev == Wrap { // invariant 3
			if w := u0 + int64(wrap.Size-uint64(r0)+uint64(r)); u != w ||
				r >= r0 || uint64(r0)-uint64(r) <= uint64(th) {
				return fmt.Errorf("invariant3: step %d", i)
			}
		}
		last = u
	}
	if _, e := New(0); !errors.Is(e, ErrInvalidThreshold) {
		return fmt.Errorf("invalid threshold low")
	}
	if _, e := New(MaxThreshold); !errors.Is(e, ErrInvalidThreshold) {
		return fmt.Errorf("invalid threshold high")
	}
	em, _ := New(th)
	if _, e := em.LastUnwrapped(); !errors.Is(e, ErrEmpty) {
		return fmt.Errorf("empty unwrapped")
	}
	if _, e := em.LastRaw(); !errors.Is(e, ErrEmpty) {
		return fmt.Errorf("empty raw")
	}
	rg, _ := New(th)
	rg.Feed(100)
	if _, e := rg.Feed(50); !errors.Is(e, ErrRegression) {
		return fmt.Errorf("regression")
	}
	if u, _ := rg.LastUnwrapped(); u != 100 {
		return fmt.Errorf("regression traced")
	}
	if _, e := wrap.Unwrap(math.MaxInt64-1, 0, 100, Forward); !errors.Is(e, ErrOverflow) {
		return fmt.Errorf("overflow")
	}
	return nil
}

func eq(a, b map[Event]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
