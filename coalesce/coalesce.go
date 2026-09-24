// Package coalesce resolves Range specs against a concrete resource length and
// normalizes them into clipped, sorted, non-overlapping, non-adjacent ranges.
//
// It depends only on rangespec.
package coalesce

import (
	"fmt"
	"sort"

	"ontology/rangespec"
)

// Interval is a closed inclusive byte interval [Start, End].
type Interval struct {
	Start int64
	End   int64
}

// Len is the number of bytes in the interval.
func (iv Interval) Len() int64 { return iv.End - iv.Start + 1 }

// UnsatisfiableError means the Range header parsed but no interval can be
// satisfied for a resource of Size bytes. Size is needed for the 416 reply.
type UnsatisfiableError struct {
	Size int64
}

func (e *UnsatisfiableError) Error() string {
	return fmt.Sprintf("coalesce: no satisfiable range for resource size %d", e.Size)
}

// Counter is a Normalize variant that records the number of interval
// comparisons performed (the counter field itself is unexported).
type Counter struct {
	cmps int
}

// Comparisons reports the number of comparisons made during the last call.
func (c *Counter) Comparisons() int { return c.cmps }

// Normalize clips each spec to [0, size-1] (never an error on overflow) and
// merges overlapping or adjacent intervals. A start at or past size, or a
// "bytes=-0" suffix, is unsatisfiable.
func Normalize(specs []rangespec.Spec, size int64) ([]Interval, error) {
	return normalize(specs, size, nil)
}

// Normalize behaves like the package-level function and counts comparisons.
func (c *Counter) Normalize(specs []rangespec.Spec, size int64) ([]Interval, error) {
	c.cmps = 0
	return normalize(specs, size, c)
}

func normalize(specs []rangespec.Spec, size int64, nr *Counter) ([]Interval, error) {
	if size < 0 {
		return nil, &UnsatisfiableError{Size: size}
	}
	ivs := make([]Interval, 0, len(specs))
	for _, sp := range specs {
		var iv Interval
		switch sp.Kind {
		case rangespec.Closed:
			if sp.A >= size {
				return nil, &UnsatisfiableError{Size: size}
			}
			iv = Interval{sp.A, sp.B}
		case rangespec.OpenEnd:
			if sp.A >= size {
				return nil, &UnsatisfiableError{Size: size}
			}
			iv = Interval{sp.A, size - 1}
		case rangespec.Suffix:
			if sp.B == 0 {
				return nil, &UnsatisfiableError{Size: size}
			}
			n := sp.B
			if n > size {
				n = size
			}
			iv = Interval{size - n, size - 1}
		}
		if iv.End >= size { // clip a closed range whose end runs past the tail
			iv.End = size - 1
		}
		ivs = append(ivs, iv)
	}

	sort.Slice(ivs, func(i, j int) bool {
		if nr != nil {
			nr.cmps++
		}
		if ivs[i].Start != ivs[j].Start {
			return ivs[i].Start < ivs[j].Start
		}
		return ivs[i].End > ivs[j].End
	})

	out := ivs[:0]
	for i, iv := range ivs {
		if i == 0 {
			out = append(out, iv)
			continue
		}
		last := &out[len(out)-1]
		if nr != nil {
			nr.cmps++
		}
		if iv.Start <= last.End+1 { // overlap or adjacency
			if iv.End > last.End {
				last.End = iv.End
			}
			continue
		}
		out = append(out, iv)
	}
	return out, nil
}
