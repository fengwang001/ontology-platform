// Package coalesce normalizes parsed range specs against a concrete
// resource size: it clips out-of-bounds ranges, sorts them by start, and
// merges overlapping and adjacent ranges. The merge is sort-based, so the
// number of range comparisons is O(n log n), never O(n^2).
package coalesce

import (
	"fmt"
	"sort"
	"sync/atomic"

	"ontology/rangespec"
)

// Range is a closed byte interval [From, To] with 0 <= From <= To.
type Range struct {
	From int64
	To   int64 // inclusive
}

// Len returns the number of bytes covered by the range.
func (r Range) Len() int64 { return r.To - r.From + 1 }

// UnsatisfiableError means that after clipping, no requested range covers
// any byte of the resource. Size is the total resource length, so callers
// can build a 416 response ("bytes */Size").
type UnsatisfiableError struct {
	Size int64
}

func (e *UnsatisfiableError) Error() string {
	return fmt.Sprintf("coalesce: no satisfiable range (resource size %d)", e.Size)
}

// cmpCount is the unexported comparison counter used to prove the O(n log n)
// bound. It is atomic because concurrent assemblers share this package.
var cmpCount atomic.Int64

// CompareCount returns the total number of range comparisons performed by
// Normalize since the last ResetCompareCount call.
func CompareCount() int64 { return cmpCount.Load() }

// ResetCompareCount zeroes the comparison counter.
func ResetCompareCount() { cmpCount.Store(0) }

// Normalize clips every spec against [0, size-1], drops empty ones, sorts
// the rest by start offset, and merges overlapping and adjacent ranges.
// The result is sorted, non-overlapping and non-adjacent. If nothing
// survives clipping it returns *UnsatisfiableError carrying the size.
func Normalize(specs []rangespec.Spec, size int64) ([]Range, error) {
	clipped := make([]Range, 0, len(specs))
	for _, sp := range specs {
		if r, ok := clip(sp, size); ok {
			clipped = append(clipped, r)
		}
	}
	if len(clipped) == 0 {
		return nil, &UnsatisfiableError{Size: size}
	}
	sort.Slice(clipped, func(i, j int) bool {
		cmpCount.Add(1)
		if clipped[i].From != clipped[j].From {
			return clipped[i].From < clipped[j].From
		}
		return clipped[i].To < clipped[j].To
	})
	out := clipped[:1]
	for _, r := range clipped[1:] {
		cmpCount.Add(1)
		last := &out[len(out)-1]
		if r.From <= last.To+1 { // overlap or adjacency
			if r.To > last.To {
				last.To = r.To
			}
		} else {
			out = append(out, r)
		}
	}
	return out, nil
}

// clip maps one spec onto the valid byte window [0, size-1]. The boolean
// result is false when the spec covers no byte of the resource.
func clip(sp rangespec.Spec, size int64) (Range, bool) {
	if size <= 0 {
		return Range{}, false
	}
	last := size - 1
	switch sp.Kind {
	case rangespec.Span:
		if sp.From > last || sp.To < sp.From {
			return Range{}, false
		}
		to := sp.To
		if to > last {
			to = last
		}
		return Range{From: sp.From, To: to}, true
	case rangespec.Open:
		if sp.From > last {
			return Range{}, false
		}
		return Range{From: sp.From, To: last}, true
	case rangespec.Suffix:
		if sp.N <= 0 {
			return Range{}, false
		}
		if sp.N >= size {
			return Range{From: 0, To: last}, true
		}
		return Range{From: size - sp.N, To: last}, true
	}
	return Range{}, false
}
