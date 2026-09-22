// Package coalesce normalizes parsed byte-range specs against a concrete
// representation length: it resolves suffix/open ranges, clips out-of-bounds
// endpoints, sorts, and merges overlapping or adjacent intervals.
package coalesce

// Interval is a closed byte interval [Start, End] inside a representation.
type Interval struct {
	Start int64
	End   int64
}

// Len returns the number of bytes covered by the interval.
func (i Interval) Len() int64 { return i.End - i.Start + 1 }

// UnsatisfiableError means every spec was syntactically valid but none can be
// applied to a representation of TotalSize bytes. TotalSize lets the caller
// build a 416 response with a matching Content-Range header.
type UnsatisfiableError struct {
	TotalSize int64
	Reason    string
}

func (e *UnsatisfiableError) Error() string {
	return "coalesce: range not satisfiable for size " + itoa(e.TotalSize) + ": " + e.Reason
}

// Normalizer sorts and merges intervals, counting pairwise comparisons.
type Normalizer struct {
	// comparisons counts every pair of intervals compared while sorting and
	// merging. It is intentionally unexported; tests inspect it via
	// ComparisonCount.
	comparisons int64
}

// ComparisonCount returns the number of interval comparisons performed by the
// most recent Normalize call.
func (n *Normalizer) ComparisonCount() int64 { return n.comparisons }

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	buf := [20]byte{}
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
