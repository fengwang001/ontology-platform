// Package backfill tracks the backfill/online phases, watermark W and the
// per-Key deduped counts. It depends only on package dedup. A View is not
// internally synchronized; package api serializes access.
package backfill

import (
	"errors"
	"fmt"

	"ontology/dedup"
)

// The four required, mutually distinct, decidable sentinel errors.
var (
	ErrInvalidW           = errors.New("backfill: watermark W must not be negative")
	ErrBackfillOutOfRange = errors.New("backfill: batch contains Seq >= W")
	ErrBackfillCompleted  = errors.New("backfill: backfill already completed")
	ErrEmptyKey           = errors.New("backfill: event Key must not be empty")
)

// Event is one upstream occurrence of Key at global site Seq.
type Event struct {
	Seq int64
	Key string
}

// View owns the materialized deduped counts and the cutover state.
type View struct {
	W         int64
	seen      *dedup.Set
	counts    map[string]int64
	owner     map[int64]string // applied Seq -> Key, used by SelfCheck
	initErr   error            // set once when W < 0; every method fails closed
	completed bool
}

// New creates a View. A negative w holds no state and makes every method
// return ErrInvalidW, so the invalid-W failure also leaves no trace.
func New(w int64) *View {
	v := &View{W: w, seen: dedup.New(), counts: map[string]int64{}, owner: map[int64]string{}}
	if w < 0 {
		v.initErr = ErrInvalidW
	}
	return v
}

// ApplyBackfill applies historical events (Seq < W). The whole batch is
// validated before any mutation, so a rejection leaves zero trace.
func (v *View) ApplyBackfill(evs []Event) error {
	if v.initErr != nil {
		return v.initErr
	}
	if v.completed {
		return ErrBackfillCompleted
	}
	for _, e := range evs {
		if e.Seq >= v.W {
			return ErrBackfillOutOfRange
		}
		if e.Key == "" {
			return ErrEmptyKey
		}
	}
	for _, e := range evs {
		v.apply(e)
	}
	return nil
}

// ApplyOnline accepts events with any Seq. Only an empty Key rejects it.
func (v *View) ApplyOnline(evs []Event) error {
	if v.initErr != nil {
		return v.initErr
	}
	for _, e := range evs {
		if e.Key == "" {
			return ErrEmptyKey
		}
	}
	for _, e := range evs {
		v.apply(e)
	}
	return nil
}

// apply is the single funnel for both streams: a Seq counts exactly when
// dedup admits it the first time, independent of stream interleaving.
func (v *View) apply(e Event) {
	if v.seen.Add(e.Seq) {
		v.counts[e.Key]++
		v.owner[e.Seq] = e.Key
	}
}

// Complete flips only the completion flag; counts are left intact so online
// increments already applied survive the cutover. Repeats are idempotent.
func (v *View) Complete() error {
	if v.initErr != nil {
		return v.initErr
	}
	v.completed = true
	return nil
}

// Counts returns a defensive copy of the per-Key deduped counts.
func (v *View) Counts() map[string]int64 {
	out := make(map[string]int64, len(v.counts))
	for k, n := range v.counts {
		out[k] = n
	}
	return out
}

// Seen returns the number of distinct applied Seq values.
func (v *View) Seen() int { return v.seen.Len() }

// SelfCheck recomputes counts from the applied (Seq, Key) pairs and verifies
// them against the maintained counts and their total against Seen.
func (v *View) SelfCheck() error {
	if v.initErr != nil {
		return v.initErr
	}
	if len(v.owner) != v.seen.Len() {
		return errors.New("backfill: applied set disagrees with dedup set")
	}
	want := map[string]int64{}
	var total int64
	for _, k := range v.owner {
		want[k]++
		total++
	}
	for k, n := range v.counts {
		if want[k] != n {
			return fmt.Errorf("backfill: key %q count %d, reference %d", k, n, want[k])
		}
	}
	if total != int64(v.seen.Len()) {
		return errors.New("backfill: counts total disagrees with Seen")
	}
	return nil
}
