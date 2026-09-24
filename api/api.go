// Package api is the public entry point of the sequence gap detector.
// It guards the det state machine with a mutex and adds the window
// validation. It depends only on package det.
package api

import (
	"errors"
	"sync"

	"ontology/det"
)

// Sentinel errors. All Feed/New failures map to one of these, and the
// three are pairwise distinguishable with errors.Is.
var (
	ErrInvalidWindow = errors.New("api: reorder window W must be > 0")
	ErrInvalidSeq    = det.ErrInvalidSeq  // seq <= 0
	ErrSeqOverflow   = det.ErrSeqOverflow // seq > MaxInt64-W
)

// Detector is the concurrency-safe gap detector.
type Detector struct {
	mu sync.Mutex
	d  *det.Detector
}

// New creates a detector with reorder window W; W <= 0 is rejected
// before any state exists.
func New(w int64) (*Detector, error) {
	if w <= 0 {
		return nil, ErrInvalidWindow
	}
	return &Detector{d: det.New(w)}, nil
}

// Feed delivers one sequence number. Safe for concurrent use.
func (d *Detector) Feed(seq int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.d.Feed(seq)
}

// Gaps returns a fresh ascending copy of confirmed gaps.
func (d *Detector) Gaps() []int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.d.Gaps()
}

// Watermark returns the current watermark H.
func (d *Detector) Watermark() int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.d.Watermark()
}

// SelfCheck runs the built-in verification of the four invariants.
func (d *Detector) SelfCheck() error { return det.SelfCheck() }
