// Package join resolves facts against the versioned dimension snapshots
// owned by package dim: locate a Key inside the snapshot of the fact's own
// version, emit a join row, and classify miss / stale / future. It depends
// only on dim.
package join

import (
	"ontology/dim"
)

// Fact is one fact-stream record. It must be joined against the snapshot of
// its Vsn, never the current table.
type Fact struct {
	Key string
	Vsn int64
}

// Row is one emitted join result.
type Row struct {
	Key string
	Val int64
	Vsn int64
}

// Outcome classifies a resolved fact. Miss and Stale are legal, non-error
// outcomes; only an empty Key or a future Vsn is an error.
type Outcome int

const (
	Emitted Outcome = iota // a Row is returned
	Missed                 // version retained, Key absent: count as miss
	Stale                  // version evicted: count as dropped
)

// Resolver binds a fact stream to one versioned dim.Store. It is stateless;
// the caller (package api) accumulates rows and counters.
type Resolver struct {
	store *dim.Store
}

// New returns a Resolver over s.
func New(s *dim.Store) *Resolver {
	return &Resolver{store: s}
}

// Resolve classifies one fact. A non-nil error (dim.ErrEmptyKey for an empty
// fact Key, dim.ErrFuture for Vsn > V) consumes no state; otherwise the
// returned Row is meaningful only when Outcome == Emitted.
func (r *Resolver) Resolve(f Fact) (Row, Outcome, error) {
	if f.Key == "" {
		return Row{}, 0, dim.ErrEmptyKey
	}
	val, kind, err := r.store.Lookup(f.Vsn, f.Key)
	if err != nil { // dim.ErrFuture
		return Row{}, 0, err
	}
	switch kind {
	case dim.Found:
		return Row{Key: f.Key, Val: val, Vsn: f.Vsn}, Emitted, nil
	case dim.Miss:
		return Row{}, Missed, nil
	default:
		return Row{}, Stale, nil
	}
}
