// Package frag tracks the received fragments of a single message:
// interval insertion, overlap/conflict detection and completeness checks.
package frag

// Interval is a half-open byte range [Start, End).
type Interval struct {
	Start int
	End   int
}

func (iv Interval) len() int { return iv.End - iv.Start }

func (iv Interval) overlaps(o Interval) bool {
	return iv.Start < o.End && o.Start < iv.End
}

// insertInterval merges n into the normalized (sorted, disjoint,
// non-adjacent) interval list, coalescing overlapping and adjacent ranges.
func insertInterval(ivals []Interval, n Interval) []Interval {
	out := make([]Interval, 0, len(ivals)+1)
	merged := n
	for _, iv := range ivals {
		switch {
		case iv.End < merged.Start:
			out = append(out, iv)
		case merged.End < iv.Start:
			out = append(out, merged)
			merged = iv
		default:
			if iv.Start < merged.Start {
				merged.Start = iv.Start
			}
			if iv.End > merged.End {
				merged.End = iv.End
			}
		}
	}
	return append(out, merged)
}
