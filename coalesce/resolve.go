package coalesce

import "ontology/rangespec"

func Resolve(spec rangespec.RangeSpec, total int64) (Interval, error) {
	if total < 0 {
		return Interval{}, &UnsatisfiableError{TotalSize: total, Reason: "negative representation length"}
	}
	switch spec.Kind {
	case rangespec.KindClosed:
		if total == 0 || spec.Start >= total {
			return Interval{}, &UnsatisfiableError{TotalSize: total, Reason: "first byte position is past the end"}
		}
		end := spec.End
		if end >= total {
			end = total - 1 // clipping, not an error
		}
		if end < spec.Start {
			return Interval{}, &UnsatisfiableError{TotalSize: total, Reason: "last byte position precedes first byte position"}
		}
		return Interval{Start: spec.Start, End: end}, nil
	case rangespec.KindOpenEnd:
		if total == 0 || spec.Start >= total {
			return Interval{}, &UnsatisfiableError{TotalSize: total, Reason: "first byte position is past the end"}
		}
		return Interval{Start: spec.Start, End: total - 1}, nil
	case rangespec.KindSuffix:
		// "-n" means the final n bytes. With n==0 the final zero bytes cover
		// no byte at all: unsatisfiable (carries the total) rather than a
		// syntax error, since the grammar itself is valid.
		if spec.Suffix <= 0 || total == 0 {
			return Interval{}, &UnsatisfiableError{TotalSize: total, Reason: "suffix range covers zero bytes"}
		}
		start := int64(0)
		if spec.Suffix < total {
			start = total - spec.Suffix
		}
		return Interval{Start: start, End: total - 1}, nil
	default:
		return Interval{}, &UnsatisfiableError{TotalSize: total, Reason: "unknown range spec kind"}
	}
}
