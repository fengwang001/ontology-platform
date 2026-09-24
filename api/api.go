// Package api is the public front of the change-stream exactly-once
// deduper. It depends only on dedup (which depends only on rec).
package api

import "ontology/dedup"

// Re-exported types: callers never need to import the inner packages.
type (
	Event  = dedup.Event
	Result = dedup.Result
	Entry  = dedup.Entry
	Kind   = dedup.Kind
)

// Outcome constants.
const (
	Applied    = dedup.Applied
	Idempotent = dedup.Idempotent
	Rejected   = dedup.Rejected
)

// The four mutually distinguishable sentinel errors.
var (
	ErrInvalidWindow  = dedup.ErrInvalidWindow  // construction argument
	ErrNegativeOffset = dedup.ErrNegativeOffset // branch ①
	ErrConflict       = dedup.ErrConflict       // branch ④
	ErrRewound        = dedup.ErrRewound        // branch ⑤
)

// Dedup is the in-process, concurrency-safe deduper.
type Dedup struct{ d *dedup.D }

// New builds a deduper with the given retention window (must be >= 1).
func New(window int) (*Dedup, error) {
	d, err := dedup.New(window)
	if err != nil {
		return nil, err
	}
	return &Dedup{d: d}, nil
}

// Apply decides a whole batch atomically: if any event is rejected the
// entire batch takes no effect and the first rejection's error is returned.
func (x *Dedup) Apply(evs []Event) ([]Result, error) {
	return x.d.ApplyBatch(evs)
}

// View snapshots every partition's retained table, entries ascending.
func (x *Dedup) View() map[int][]Entry { return x.d.View() }

// SelfCheck verifies the four invariants on this instance and on built-in
// random sequences.
func (x *Dedup) SelfCheck() error { return x.d.SelfCheck() }
