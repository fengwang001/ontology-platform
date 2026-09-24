// Package det is the gap-detector state machine: contiguous prefix, in-flight
// set and confirmed gap set, converged per the fixed rules on every Feed.
// It depends only on package seq.
package det

import (
	"sync"

	"ontology/seq"
)

// Detector is the in-memory state machine. The zero value is not usable;
// create it with New.
type Detector struct {
	mu sync.Mutex

	w       int64 // reorder window, > 0
	h       int64 // watermark: 1..h all seen or declared gaps
	seen    map[int64]struct{}
	maxSeen int64 // largest key in seen; meaningful only when seen is non-empty
	gaps    []int64

	// checks counts sequence positions examined/compared during the
	// convergence of the most recent Feed. Unexported by design: it must
	// never be reachable through the public API.
	checks int64
}

// New creates a detector with reorder window w. Precondition: w > 0
// (the api layer rejects non-positive windows before calling this).
func New(w int64) *Detector {
	return &Detector{w: w, seen: make(map[int64]struct{})}
}

// Feed admits one sequence number and converges. It returns seq.ErrInvalidSeq
// for seq <= 0 and seq.ErrSeqOverflow when seq > MaxInt64-w; in either error
// case no state changes.
func (d *Detector) Feed(s int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.checks = 0
	if !seq.Valid(s) {
		return seq.ErrInvalidSeq
	}
	if seq.Overflow(s, d.w) {
		return seq.ErrSeqOverflow
	}
	if seq.Covered(s, d.h) {
		return nil // already in prefix or gaps: ignore, touch nothing
	}

	d.seen[s] = struct{}{}
	if s > d.maxSeen {
		d.maxSeen = s
	}

	for {
		next := d.h + 1
		d.checks++ // examine whether next belongs to seen
		if _, ok := d.seen[next]; ok {
			delete(d.seen, next)
			d.h++
			continue
		}
		if len(d.seen) == 0 {
			d.maxSeen = 0
			break
		}
		d.checks++ // window comparison for the missing position
		if seq.MissedWindow(next, d.maxSeen, d.w) {
			d.gaps = append(d.gaps, next)
			d.h++
			continue
		}
		break
	}
	return nil
}

// Gaps returns confirmed gap numbers in ascending order.
func (d *Detector) Gaps() []int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]int64, len(d.gaps))
	copy(out, d.gaps) // gaps are appended in strictly ascending order
	return out
}

// Watermark returns H, the upper bound of the confirmed contiguous prefix.
func (d *Detector) Watermark() int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.h
}
