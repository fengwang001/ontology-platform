// Package stream is the public facade tying together hashfam, sketch, bound
// and topk: Add, Estimate, HeavyHitters, Merge and SelfCheck.
package stream

import (
	"errors"

	"ontology/bound"
	"ontology/sketch"
	"ontology/topk"
)

// ErrCorrupt reports a failed SelfCheck.
var ErrCorrupt = errors.New("stream: internal consistency check failed")

// Stream couples a sketch with a heavy-hitter tracker. Read-only methods
// (Estimate, HeavyHitters, SelfCheck) may run concurrently after the writes
// are done.
type Stream struct {
	s  *sketch.Sketch
	tk *topk.Tracker
}

// New builds a stream from error parameters (eps, delta) in (0,1).
// maxTotal caps the total count; 0 means no cap.
func New(eps, delta float64, threshold, maxTotal uint64) (*Stream, error) {
	w, d, err := bound.Params(eps, delta)
	if err != nil {
		return nil, err
	}
	return NewWithDims(w, d, threshold, maxTotal)
}

// NewWithDims builds a stream from explicit dimensions.
func NewWithDims(w, d int, threshold, maxTotal uint64) (*Stream, error) {
	s, err := sketch.New(w, d, maxTotal)
	if err != nil {
		return nil, err
	}
	return &Stream{s: s, tk: topk.New(s, threshold)}, nil
}

// Add counts n occurrences of key. Rejected adds leave no trace anywhere.
func (st *Stream) Add(key string, n uint64) error {
	if err := st.s.Add(key, n); err != nil {
		return err
	}
	st.tk.Add(key)
	return nil
}

// Estimate returns the approximate count of key, never below the true count.
func (st *Stream) Estimate(key string) (uint64, error) { return st.s.Estimate(key) }

// HeavyHitters returns all keys with Estimate >= threshold, sorted.
func (st *Stream) HeavyHitters() []string { return st.tk.HeavyHitters() }

// Merge folds other into st. On rejection neither stream is modified.
func (st *Stream) Merge(o *Stream) error {
	if st.tk.Threshold() != o.tk.Threshold() {
		return sketch.ErrIncompatible
	}
	if err := st.s.Merge(o.s); err != nil {
		return err
	}
	st.tk.Absorb(o.tk)
	return nil
}

// SelfCheck verifies: dimensions are positive, every cell is non-negative
// (guaranteed by the uint64 cell type), and each row sums to the total.
func (st *Stream) SelfCheck() error {
	w, d := st.s.Dims()
	if w <= 0 || d <= 0 {
		return ErrCorrupt
	}
	for r := 0; r < d; r++ {
		if st.s.RowSum(r) != st.s.Total() {
			return ErrCorrupt
		}
	}
	return nil
}
