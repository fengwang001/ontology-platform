// Package api is the public face of the sequence gap detector: construction,
// feeding sequence numbers, reading watermark/gaps and self-checking.
// It depends only on package det.
package api

import (
	"errors"
	"fmt"
	"math"

	"ontology/det"
	"ontology/seq"
)

// Sentinel errors; all failure modes are distinguishable via errors.Is.
var (
	// ErrInvalidWindow is returned by New when the reorder window is <= 0.
	ErrInvalidWindow = errors.New("api: invalid reorder window: must be > 0")
	// ErrInvalidSeq is returned by Feed for seq <= 0.
	ErrInvalidSeq = seq.ErrInvalidSeq
	// ErrSeqOverflow is returned by Feed when seq > MaxInt64 - W.
	ErrSeqOverflow = seq.ErrSeqOverflow
	// ErrSelfCheck wraps any invariant violation reported by SelfCheck.
	ErrSelfCheck = errors.New("api: self-check found an invariant violation")
)

// Detector detects confirmed gaps in an out-of-order, duplicate, lossy
// sequence stream. State lives in process memory.
type Detector struct {
	d *det.Detector
}

// New builds a detector with reorder window w (w must be > 0).
func New(w int64) (*Detector, error) {
	if w <= 0 {
		return nil, ErrInvalidWindow
	}
	return &Detector{d: det.New(w)}, nil
}

// Feed admits one sequence number.
func (d *Detector) Feed(seq int64) error { return d.d.Feed(seq) }

// Gaps returns confirmed gap sequence numbers in ascending order.
func (d *Detector) Gaps() []int64 { return d.d.Gaps() }

// Watermark returns H: every number in 1..H is either seen or a confirmed gap.
func (d *Detector) Watermark() int64 { return d.d.Watermark() }

// SelfCheck replays the built-in event sequences on fresh detectors and
// verifies the four invariants; it never touches the receiver's state.
func (d *Detector) SelfCheck() error {
	const w = int64(2)
	events := []int64{1, 2, 3, 4, 6, 9, 2, 3, 6, 10, 11}
	wantH := []int64{1, 2, 3, 4, 4, 7, 7, 7, 7, 10, 11}
	wantGaps := [][]int64{
		nil, nil, nil, nil, nil,
		{5, 7}, {5, 7}, {5, 7}, {5, 7},
		{5, 7, 8}, {5, 7, 8},
	}
	c, err := New(w)
	if err != nil {
		return err
	}
	for i, e := range events { // invariants 1-3 against the reference table
		if err := c.Feed(e); err != nil {
			return fmt.Errorf("%w: step %d unexpected error: %v", ErrSelfCheck, i+1, err)
		}
		if c.Watermark() != wantH[i] || !equal(c.Gaps(), wantGaps[i]) {
			return fmt.Errorf("%w: step %d got H=%d gaps=%v, want H=%d gaps=%v",
				ErrSelfCheck, i+1, c.Watermark(), c.Gaps(), wantH[i], wantGaps[i])
		}
	}

	r, _ := New(w) // invariant 4: rejected Feed leaves no trace
	if err := r.Feed(0); !errors.Is(err, ErrInvalidSeq) {
		return fmt.Errorf("%w: seq<=0: %v", ErrSelfCheck, err)
	}
	if r.Watermark() != 0 || len(r.Gaps()) != 0 {
		return fmt.Errorf("%w: rejected Feed changed state", ErrSelfCheck)
	}
	if err := r.Feed(math.MaxInt64); !errors.Is(err, ErrSeqOverflow) {
		return fmt.Errorf("%w: overflow: %v", ErrSelfCheck, err)
	}
	if r.Watermark() != 0 || len(r.Gaps()) != 0 {
		return fmt.Errorf("%w: overflow Feed changed state", ErrSelfCheck)
	}
	if err := r.Feed(1); err != nil || r.Watermark() != 1 { // still usable
		return fmt.Errorf("%w: detector unusable after rejection", ErrSelfCheck)
	}
	if _, err := New(0); !errors.Is(err, ErrInvalidWindow) {
		return fmt.Errorf("%w: New(0): %v", ErrSelfCheck, err)
	}
	if _, err := New(-3); !errors.Is(err, ErrInvalidWindow) {
		return fmt.Errorf("%w: New(-3): %v", ErrSelfCheck, err)
	}
	return nil
}

func equal(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
