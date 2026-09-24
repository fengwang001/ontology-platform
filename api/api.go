// Package api is the public entry point of the offset/timestamp
// bidirectional index. It serializes access to bidx and enforces the
// append capacity. It depends only on bidx.
package api

import (
	"errors"
	"math"
	"sync"

	"ontology/bidx"
)

// ErrCapacity is returned when Append would exceed maxRecords. It is
// distinct from bidx's sentinels so every failure is classifiable.
var ErrCapacity = errors.New("api: append exceeds max records")

// Index is safe for concurrent use.
type Index struct {
	mu   sync.RWMutex
	maxN int
	log  *bidx.Index
}

// New creates an index that accepts at most maxRecords appends.
func New(maxRecords int) *Index {
	return &Index{maxN: maxRecords, log: bidx.New()}
}

// Append appends one timestamp. It validates capacity before touching
// any state, so a rejected append leaves no trace.
func (x *Index) Append(ts int64) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.log.Len() >= x.maxN {
		return ErrCapacity
	}
	x.log.Append(ts)
	return nil
}

func (x *Index) TSAt(off int64) (int64, error) {
	x.mu.RLock()
	defer x.mu.RUnlock()
	return x.log.TSAt(off)
}

func (x *Index) SafeOff(T int64) (int64, bool, error) {
	x.mu.RLock()
	defer x.mu.RUnlock()
	return x.log.SafeOff(T)
}

func (x *Index) Len() int {
	x.mu.RLock()
	defer x.mu.RUnlock()
	return x.log.Len()
}

// naiveSafeOff is the plain linear-scan reference: the largest o with
// max(ts[0..o]) <= T.
func naiveSafeOff(ts []int64, T int64) (int64, bool) {
	run := int64(0)
	res, found := int64(-1), false
	for o, v := range ts {
		if o == 0 || v > run {
			run = v
		}
		if run <= T {
			res, found = int64(o), true
		}
	}
	return res, found
}

// SelfCheck replays a built-in operation sequence and verifies the four
// invariants: forward precision, agreement with the naive reference,
// non-decreasing prefix maxima (surfaced through binary-vs-naive
// agreement over a value sweep), and no-trace rejection.
func (x *Index) SelfCheck() error {
	seq := []int64{2, 1, 8, 3, 4, 9, 5, 7}
	c := New(len(seq))
	for _, ts := range seq {
		if err := c.Append(ts); err != nil {
			return err
		}
	}
	for o, want := range seq { // invariant 1
		if got, err := c.TSAt(int64(o)); err != nil || got != want {
			return errors.New("api: selfcheck forward precision")
		}
	}
	tset := map[int64]bool{math.MinInt64: true, math.MaxInt64: true, 0: true}
	for _, v := range seq {
		tset[v-1], tset[v], tset[v+1] = true, true, true
	}
	for T := range tset { // invariants 2 and 3
		got, found, err := c.SafeOff(T)
		if err != nil {
			return err
		}
		want, wfound := naiveSafeOff(seq, T)
		if got != want || found != wfound {
			return errors.New("api: selfcheck safeoff mismatch")
		}
	}
	if err := rejectedNoTrace(c); err != nil { // invariant 4
		return err
	}
	return nil
}

func rejectedNoTrace(c *Index) error {
	before := c.Len()
	if err := c.Append(100); !errors.Is(err, ErrCapacity) {
		return errors.New("api: selfcheck capacity rejection")
	}
	if _, err := c.TSAt(int64(before)); !errors.Is(err, bidx.ErrOutOfRange) {
		return errors.New("api: selfcheck range rejection")
	}
	e := New(8)
	if _, _, err := e.SafeOff(0); !errors.Is(err, bidx.ErrEmptyLog) {
		return errors.New("api: selfcheck empty rejection")
	}
	if e.Len() != 0 || c.Len() != before {
		return errors.New("api: selfcheck rejection left a trace")
	}
	return nil
}
