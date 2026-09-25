// Package check is a naive reference implementation of interval
// merging: it keeps an ordered slice and rescans it segment by
// segment. Tests for package merge live here and depend on merge.
package check

import (
	"cmp"
	"slices"

	"ontology/iv"
)

// Ref is a naive reference merger used to cross-check package merge.
// The zero value is ready to use.
type Ref struct {
	ranges []iv.Interval
}

// Add appends v and renormalizes the whole slice from scratch.
func (r *Ref) Add(v iv.Interval) error {
	if err := v.Validate(); err != nil {
		return err
	}
	r.ranges = append(r.ranges, v)
	r.normalize()
	return nil
}

// AddAll adds every interval in order.
func (r *Ref) AddAll(vs []iv.Interval) error {
	for _, v := range vs {
		if err := r.Add(v); err != nil {
			return err
		}
	}
	return nil
}

// normalize sorts by Start and sweeps once, merging whenever the
// previous interval's End >= the next Start (abutting merges too).
func (r *Ref) normalize() {
	slices.SortFunc(r.ranges, func(a, b iv.Interval) int {
		return cmp.Compare(a.Start, b.Start)
	})
	out := r.ranges[:0]
	for _, v := range r.ranges {
		if n := len(out); n > 0 && out[n-1].End >= v.Start {
			out[n-1].End = max(out[n-1].End, v.End)
		} else {
			out = append(out, v)
		}
	}
	r.ranges = out
}

// Ranges returns a copy of the merged intervals.
func (r *Ref) Ranges() []iv.Interval {
	return slices.Clone(r.ranges)
}
