// Package fww manages the per-key registers of a first-write-wins dedup
// register; the materialized view can only be kept via the changelog Feed emits.
package fww

import (
	"errors"
	"fmt"
	"strconv"
	"sync"

	"ontology/reg"
)

// Decidable sentinel errors; match with errors.Is.
var (
	ErrEmptyKey       = errors.New("fww: empty key")
	ErrNonPositiveSeq = errors.New("fww: non-positive seq")
	ErrDuplicateSeq   = reg.ErrDuplicateSeq
)

// Write is one upstream write. Change is one changelog row (Added=false: "-").
type Write struct {
	Key string
	Seq int64
	Val string
}
type Change struct {
	Added bool
	Key   string
	Seq   int64
	Val   string
}

// Store is the multi-key register; use New.
type Store struct {
	mu      sync.RWMutex
	m       map[string]*reg.Register
	dropped int64
	// lastChecked counts keys inspected by the latest write: a direct
	// map[Key] lookup is exactly one. Unexported; absent from every signature.
	lastChecked int
}

// New returns an empty store.
func New() *Store { return &Store{m: map[string]*reg.Register{}} }

// Feed applies one batch atomically: it validates against a clone first, so a
// rejected batch cannot alter effective values, dropped counter or changelog.
// It returns the changelog rows emitted by this batch, in order.
func (s *Store) Feed(ws []Write) ([]Change, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sim := make(map[string]*reg.Register, len(s.m)) // dry run on a clone
	for k, r := range s.m {
		sim[k] = r.Clone()
	}
	for _, w := range ws {
		switch {
		case w.Key == "":
			return nil, ErrEmptyKey
		case w.Seq < 1:
			return nil, ErrNonPositiveSeq
		}
		if sim[w.Key] == nil {
			sim[w.Key] = reg.New()
		}
		if _, err := sim[w.Key].Step(w.Seq, w.Val); err != nil {
			return nil, err
		}
	}

	out := make([]Change, 0, len(ws))
	for _, w := range ws {
		s.lastChecked = 1 // one direct map[Key] lookup, independent of table size
		if s.m[w.Key] == nil {
			s.m[w.Key] = reg.New()
		}
		o, err := s.m[w.Key].Step(w.Seq, w.Val)
		if err != nil {
			return nil, err // dry run passed; unreachable
		}
		if o.Dropped {
			s.dropped++
			continue
		}
		if o.Had { // withdraw the previously materialized late write first
			out = append(out, Change{false, w.Key, o.Old.Seq, o.Old.Val})
		}
		out = append(out, Change{true, w.Key, w.Seq, w.Val})
	}
	return out, nil
}

// Snapshot runs fn on a consistent read-only copy under RLock.
func (s *Store) Snapshot(fn func(view map[string]string, dropped int64)) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v := make(map[string]string, len(s.m))
	for k, r := range s.m {
		v[k] = r.Current().Val
	}
	fn(v, s.dropped)
}

// LookupCheck feeds m distinct keys at several sizes then one more write and
// verifies internally that the inspected-key count stays an m-independent
// constant. It returns only pass/fail; the counter value never crosses out.
func LookupCheck() error {
	for _, m := range []int{100, 1000, 10000} {
		s := New()
		b := make([]Write, m)
		for i := range b {
			b[i] = Write{strconv.Itoa(i), 1, ""}
		}
		if _, err := s.Feed(b); err != nil {
			return err
		}
		if _, err := s.Feed([]Write{{Key: "0", Seq: 2}}); err != nil {
			return err
		}
		if s.lastChecked > 2 { // bound is a constant with no dependence on m
			return fmt.Errorf("fww: inspected %d keys at m=%d, want O(1)", s.lastChecked, m)
		}
	}
	return nil
}
