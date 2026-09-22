// Package coalesce normalizes parsed range specs against a known resource
// length: it clips out-of-bounds ranges, sorts them, and merges overlapping
// and adjacent ranges into a minimal disjoint list.
package coalesce

import (
	"fmt"
	"sort"
	"sync/atomic"

	"ontology/rangespec"
)

// Range is an inclusive byte range [First, Last] with 0 <= First <= Last.
type Range struct {
	First int64
	Last  int64
}

// Len returns the number of bytes covered by the range.
func (r Range) Len() int64 { return r.Last - r.First + 1 }

// UnsatisfiableError reports that no requested range overlaps the resource.
// Total carries the resource length so callers can build a 416 response.
type UnsatisfiableError struct {
	Total int64
}

func (e *UnsatisfiableError) Error() string {
	return fmt.Sprintf("coalesce: no satisfiable range (resource length %d)", e.Total)
}

// compareCount is the unexported instrumentation counter for requirement 4:
// it records every comparison the normalization algorithm performs, so tests
// can prove the count grows as O(n log n), not O(n^2). It is only mutated
// inside Normalize and only read through the accessors below.
var compareCount atomic.Int64

// CompareCount returns the cumulative number of range comparisons performed
// by Normalize since the last ResetCompareCount call.
func CompareCount() int64 { return compareCount.Load() }

// ResetCompareCount zeroes the comparison counter.
func ResetCompareCount() { compareCount.Store(0) }

// Normalize clips every spec to [0, total), drops empty ones, sorts the rest
// by start offset, and merges overlapping and adjacent ranges. The result is
// a sorted list in which consecutive ranges are disjoint and non-adjacent.
//
// If no spec survives clipping, it returns *UnsatisfiableError.
func Normalize(specs []rangespec.Spec, total int64) ([]Range, error) {
	ranges := make([]Range, 0, len(specs))
	for _, s := range specs {
		if r, ok := clip(s, total); ok {
			ranges = append(ranges, r)
		}
	}
	if len(ranges) == 0 {
		return nil, &UnsatisfiableError{Total: total}
	}
	sort.Slice(ranges, func(i, j int) bool {
		compareCount.Add(1)
		if ranges[i].First != ranges[j].First {
			return ranges[i].First < ranges[j].First
		}
		return ranges[i].Last < ranges[j].Last
	})
	merged := ranges[:1]
	for _, r := range ranges[1:] {
		last := &merged[len(merged)-1]
		compareCount.Add(1)
		if r.First <= last.Last+1 { // overlapping or adjacent
			if r.Last > last.Last {
				last.Last = r.Last
			}
		} else {
			merged = append(merged, r)
		}
	}
	return merged, nil
}

// clip converts one spec into an absolute range bounded to [0, total).
// The second result is false when the spec covers no byte of the resource.
func clip(s rangespec.Spec, total int64) (Range, bool) {
	if total <= 0 {
		return Range{}, false
	}
	if s.IsSuffix() {
		// "-n" means the last n bytes; n == 0 covers nothing, and
		// n > total is clipped to the whole resource.
		n := s.Suffix
		if n == 0 {
			return Range{}, false
		}
		if n > total {
			n = total
		}
		return Range{First: total - n, Last: total - 1}, true
	}
	// A start beyond the last byte is the only "unsatisfiable" case for
	// the a-b / a- forms; an over-long end is clipped, not an error.
	if s.First >= total {
		return Range{}, false
	}
	last := s.Last
	if last < 0 || last >= total {
		last = total - 1
	}
	return Range{First: s.First, Last: last}, true
}
