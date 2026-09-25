// Package api is the public entry point to the latest-per-key log
// compactor. It depends on package compact and, through it, package rec.
package api

import (
	"errors"

	"ontology/compact"
	"ontology/rec"
)

// Sentinel errors. The four rejection causes are always distinct:
// ErrBadRetention, ErrEmptyKey, ErrNegativeTS here, and compact.ErrBadWindow
// returned by Compact when lo >= hi.
var (
	ErrBadRetention = errors.New("api: negative retention period")
	ErrEmptyKey     = errors.New("api: record has empty key")
	ErrNegativeTS   = errors.New("api: record has negative timestamp")
)

// API is an in-memory compactor. All state lives behind compact.C, which
// is safe for concurrent use.
type API struct {
	c *compact.C
}

// New builds a compactor with tombstone retention period R (R >= 0).
func New(retention int64) (*API, error) {
	if retention < 0 {
		return nil, ErrBadRetention
	}
	return &API{c: compact.New(retention)}, nil
}

// Feed appends records. The whole batch is validated before any state
// changes, so an invalid record rejects the entire batch atomically.
func (a *API) Feed(rs []rec.Rec) error {
	for _, r := range rs {
		if r.Key == "" {
			return ErrEmptyKey
		}
		if r.TS < 0 {
			return ErrNegativeTS
		}
	}
	a.c.Add(rs)
	return nil
}

// Compact folds half-open window [lo,hi). It returns compact.ErrBadWindow
// when lo >= hi; rejection leaves fed state untouched.
func (a *API) Compact(lo, hi int64) ([]rec.Rec, error) {
	return a.c.Compact(lo, hi)
}

// View returns every fed record so far (a sorted copy).
func (a *API) View() []rec.Rec { return a.c.View() }

// SelfCheck verifies the four invariants on a built-in record set.
func (a *API) SelfCheck() error {
	v, err := New(5)
	if err != nil {
		return err
	}
	// a: put ts1 folded by a tomb ts3 that retention later drops (8<=10).
	// b: put ts2 folded by a tomb ts9 that retention keeps (14>10).
	rs := []rec.Rec{
		{Key: "a", Value: 1, TS: 1},
		{Key: "b", Value: 10, TS: 2},
		{Key: "a", Value: 0, TS: 3, Del: true},
		{Key: "b", Value: 0, TS: 9, Del: true},
	}
	if err := v.Feed(rs); err != nil {
		return err
	}
	lo, hi := int64(0), int64(10)
	got, err := v.Compact(lo, hi)
	if err != nil {
		return err
	}
	// Invariant 1: identical to naive recomputation.
	if want := compact.Naive(v.View(), lo, hi, 5); !equal(got, want) {
		return errors.New("api selfcheck: mismatch with naive recomputation")
	}
	// Invariant 2: at most one output per key.
	seen := map[string]bool{}
	for _, r := range got {
		if seen[r.Key] {
			return errors.New("api selfcheck: duplicate key in result")
		}
		seen[r.Key] = true
	}
	// Invariant 3: no resurrection. a must be absent (dropped tomb folded
	// its earlier put away); b must be its kept tombstone.
	if _, ok := lookup(got, "a"); ok {
		return errors.New("api selfcheck: dropped tombstone resurrected an earlier put")
	}
	br, ok := lookup(got, "b")
	if !ok || !br.Del || br.TS != 9 {
		return errors.New("api selfcheck: surviving tombstone mishandled")
	}
	// Invariant 4: rejected operations leave no trace.
	before := v.View()
	if _, err := New(-1); !errors.Is(err, ErrBadRetention) {
		return errors.New("api selfcheck: R<0 not rejected")
	}
	if err := v.Feed([]rec.Rec{{Key: "", TS: 1}}); !errors.Is(err, ErrEmptyKey) {
		return errors.New("api selfcheck: empty key not rejected")
	}
	if err := v.Feed([]rec.Rec{{Key: "z", TS: -1}}); !errors.Is(err, ErrNegativeTS) {
		return errors.New("api selfcheck: negative TS not rejected")
	}
	if _, err := v.Compact(hi, lo); !errors.Is(err, compact.ErrBadWindow) {
		return errors.New("api selfcheck: lo>=hi not rejected")
	}
	if !equal(v.View(), before) {
		return errors.New("api selfcheck: rejected operation changed state")
	}
	if _, err := v.Compact(lo, hi); err != nil {
		return errors.New("api selfcheck: instance unusable after rejection")
	}
	return nil
}

func equal(x, y []rec.Rec) bool {
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

func lookup(rs []rec.Rec, key string) (rec.Rec, bool) {
	for _, r := range rs {
		if r.Key == key {
			return r, true
		}
	}
	return rec.Rec{}, false
}
