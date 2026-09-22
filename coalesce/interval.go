package coalesce

import (
	"errors"

	"ontology/rangespec"
)

// Interval is a half-open byte range [Start, End) over a resource of a known
// size. Normalized output uses inclusive byte positions, but half-open
// intervals make length and adjacency arithmetic trivial.
type Interval struct {
	Start int64
	End   int64
}

// Length returns the number of bytes in the interval.
func (i Interval) Length() int64 { return i.End - i.Start }

// UnsatisfiableError means the request was syntactically valid but no byte
// range it names overlaps the representation. Size is the current resource
// size so callers can synthesize a 416 response with Content-Range.
type UnsatisfiableError struct {
	Size int64
}

func (e *UnsatisfiableError) Error() string {
	return "requested range not satisfiable for resource of size " + itoa(e.Size)
}

// AsUnsatisfiable extracts a *UnsatisfiableError.
func AsUnsatisfiable(err error) (*UnsatisfiableError, bool) {
	var ue *UnsatisfiableError
	if errors.As(err, &ue) {
		return ue, true
	}
	return nil, false
}

// Resolve turns raw specs into concrete half-open intervals of a size-byte
// resource. Out-of-range endpoints are clipped rather than rejected; only a
// request that ends up covering no byte at all is unsatisfiable.
//
// "a-b" clips b to size-1; "a-" extends to size; "-n" takes the final n bytes
// (the whole resource when n >= size) and "-0" denotes the final zero bytes,
// i.e. the empty suffix, which can never be satisfied.
func Resolve(specs []rangespec.Spec, size int64) ([]Interval, error) {
	out := make([]Interval, 0, len(specs))
	for _, s := range specs {
		switch s.Kind {
		case rangespec.Closed:
			if s.First >= size {
				continue
			}
			end := s.Last + 1
			if end > size {
				end = size
			}
			out = append(out, Interval{Start: s.First, End: end})
		case rangespec.From:
			if s.First >= size {
				continue
			}
			out = append(out, Interval{Start: s.First, End: size})
		case rangespec.Suffix:
			if s.First <= 0 {
				continue
			}
			start := size - s.First
			if start < 0 {
				start = 0
			}
			out = append(out, Interval{Start: start, End: size})
		}
	}
	if len(out) == 0 {
		return nil, &UnsatisfiableError{Size: size}
	}
	return out, nil
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	u := uint64(n)
	if neg {
		u = uint64(-n)
	}
	var buf [20]byte
	i := len(buf)
	for u > 0 {
		i--
		buf[i] = byte('0' + u%10)
		u /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
