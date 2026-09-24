// Package det is the gap-detector state machine: Feed convergence,
// watermark, in-flight set and confirmed gaps. Depends only on seq.
package det

import (
	"errors"
	"maps"
	"math"
	"slices"

	"ontology/seq"
)

// Failure modes detected here; invalid window is raised by api in New.
var (
	ErrInvalidSeq  = errors.New("det: sequence number must be > 0")
	ErrSeqOverflow = errors.New("det: sequence number exceeds MaxInt64-W")
)

// Detector holds all state in process memory. Construct with New.
type Detector struct {
	w    int64
	h    int64              // watermark: 1..h all seen or judged gaps
	seen map[int64]struct{} // arrivals > h; post-convergence max-h <= w
	gaps []int64            // append-only, ascending (each new gap = h+1)
	// steps counts numbers examined while converging the last Feed.
	// Unexported; SelfCheck returns a bare error, so it never leaks out.
	steps int
}

// New constructs a Detector; api rejects w <= 0 before calling.
func New(w int64) *Detector { return &Detector{w: w, seen: map[int64]struct{}{}} }

// Feed delivers one number and converges until nothing moves.
func (d *Detector) Feed(s int64) error {
	d.steps = 0
	if !seq.Valid(s) {
		return ErrInvalidSeq
	}
	if !seq.InRange(s, d.w) {
		return ErrSeqOverflow
	}
	if seq.Covered(s, d.h) {
		return nil // already in prefix (seen or gap): touch nothing
	}
	d.seen[s] = struct{}{}
	for {
		d.steps++ // examine the single candidate h+1
		next := d.h + 1
		if _, ok := d.seen[next]; ok {
			delete(d.seen, next) // rule 1: extend contiguous prefix
			d.h = next
			continue
		}
		mx := d.maxSeen()
		d.steps += len(d.seen) // locating max(seen); window-bounded
		if seq.Overdue(next, mx, d.w) {
			d.gaps = append(d.gaps, next) // rule 2: h+1 missed window
			d.h = next
			continue
		}
		return nil // rule 3: wait inside the window
	}
}

// maxSeen is independent of history: seen holds <= w+1 entries.
func (d *Detector) maxSeen() int64 {
	var mx int64
	for s := range d.seen {
		if s > mx {
			mx = s
		}
	}
	return mx
}

// Gaps returns a fresh ascending copy of confirmed gaps.
func (d *Detector) Gaps() []int64 { return append([]int64(nil), d.gaps...) }

// Watermark returns h.
func (d *Detector) Watermark() int64 { return d.h }

// Seen returns a sorted snapshot of the in-flight set. Internal surface;
// the public api package does not expose it.
func (d *Detector) Seen() []int64 { return slices.Sorted(maps.Keys(d.seen)) }

// SelfCheck verifies the four invariants on built-in sequences with
// fresh detectors. Returns a bare error, so no counter value leaks out.
func SelfCheck() error {
	d := New(2)
	evs := []int64{1, 2, 3, 4, 6, 9, 2, 3, 6, 10, 11}
	// (H,gaps) checkpoints after steps 5, 6, 9, 11 (indices 4,5,8,10).
	want := map[int][2][]int64{
		4: {{4}, nil}, 5: {{7}, {5, 7}}, 8: {{7}, {5, 7}}, 10: {{11}, {5, 7, 8}},
	}
	for i, s := range evs {
		if err := d.Feed(s); err != nil {
			return err
		}
		if err := checkDisjoint(d); err != nil { // invariant 1 (disjoint half)
			return err
		}
		if w, ok := want[i]; ok && (d.h != w[0][0] || !slices.Equal(d.gaps, w[1])) {
			return errors.New("det: 11-step walk mismatch") // invariants 2/3
		}
	}
	c := New(2)
	_, _ = c.Feed(1), c.Feed(3)
	h0, g0, s0 := c.h, len(c.gaps), len(c.seen)
	for _, s := range []int64{0, -1, math.MaxInt64} {
		err := c.Feed(s) // invariant 4
		if err == nil || c.h != h0 || len(c.gaps) != g0 || len(c.seen) != s0 {
			return errors.New("det: rejected feed left a trace")
		}
	}
	for _, m := range []int64{100, 1000, 10000} { // O(1) convergence
		b := New(2)
		for s := int64(1); s <= m; s++ {
			_ = b.Feed(s)
		}
		if err := b.Feed(m + 1); err != nil || b.steps > 4 {
			return errors.New("det: convergence work is not O(1)")
		}
	}
	return nil
}

// checkDisjoint verifies the pairwise-disjoint half of invariant 1:
// gaps never leave the prefix and seen never enters it. (Completeness
// against the naive full-set reference is pinned by TestNaiveEquivalence;
// numbers still inside the reorder window are legitimately pending.)
func checkDisjoint(d *Detector) error {
	for _, g := range d.gaps {
		if g > d.h {
			return errors.New("det: gap ahead of watermark")
		}
	}
	for x := range d.seen {
		if x <= d.h {
			return errors.New("det: seen number inside prefix")
		}
	}
	return nil
}
