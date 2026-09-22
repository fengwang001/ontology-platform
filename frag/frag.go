// Package frag tracks the received byte intervals of a single message
// and decides overlap conflicts, merges and completeness.
package frag

// Interval is a half-open byte range [Start, End).
type Interval struct {
	Start int
	End   int
}

// Set holds the fragments received so far for one message of a fixed
// total length. The zero value is not usable; construct with NewSet.
type Set struct {
	total int
	data  []byte
	ivs   []Interval // normalized: ascending, non-overlapping, non-adjacent
	got   int
}

// NewSet creates a Set for a message of total bytes.
func NewSet(total int) (*Set, error) {
	if total <= 0 {
		return nil, ErrZeroTotal
	}
	return &Set{total: total, data: make([]byte, total)}, nil
}

// Total returns the declared total message length.
func (s *Set) Total() int { return s.total }

// Received returns the number of distinct bytes received so far.
func (s *Set) Received() int { return s.got }

// Complete reports whether [0, total) is fully covered.
func (s *Set) Complete() bool { return s.got == s.total }

// Intervals returns the normalized list of received intervals:
// ascending, mutually non-overlapping and non-adjacent.
func (s *Set) Intervals() []Interval {
	out := make([]Interval, len(s.ivs))
	copy(out, s.ivs)
	return out
}

// Plan validates a fragment at [off, off+len(data)) without mutating
// the Set. It reports how many previously unreceived bytes the
// fragment would add. Identical retransmits plan zero new bytes.
func (s *Set) Plan(off int, data []byte) (int, error) {
	if len(data) == 0 {
		return 0, ErrEmptyFragment
	}
	if off < 0 || off+len(data) > s.total {
		return 0, ErrOutOfRange
	}
	if err := s.checkConflict(off, data); err != nil {
		return 0, err
	}
	return s.newBytes(off, off+len(data)), nil
}

// Apply inserts a fragment that has been validated by Plan. Callers
// must invoke Plan with the same arguments first and only Apply when
// Plan succeeded; reasm relies on this two-phase split to keep budget
// rejections side-effect free.
func (s *Set) Apply(off int, data []byte) {
	end := off + len(data)
	copy(s.data[off:end], data)
	s.ivs = merge(s.ivs, Interval{Start: off, End: end})
	s.got = 0
	for _, iv := range s.ivs {
		s.got += iv.End - iv.Start
	}
}

// Bytes returns a copy of the assembled message. It is only
// meaningful when Complete reports true.
func (s *Set) Bytes() []byte {
	out := make([]byte, s.total)
	copy(out, s.data)
	return out
}

// checkConflict compares the fragment against already received bytes
// and returns a ConflictError spanning the differing region.
func (s *Set) checkConflict(off int, data []byte) error {
	lo, hi := -1, -1
	for _, iv := range s.ivs {
		start := max(iv.Start, off)
		end := min(iv.End, off+len(data))
		for i := start; i < end; i++ {
			if s.data[i] != data[i-off] {
				if lo < 0 {
					lo = i
				}
				hi = i + 1
			}
		}
	}
	if lo < 0 {
		return nil
	}
	return &ConflictError{Start: lo, End: hi}
}

// newBytes counts positions in [start, end) not yet covered.
func (s *Set) newBytes(start, end int) int {
	n := end - start
	for _, iv := range s.ivs {
		n -= max(0, min(iv.End, end)-max(iv.Start, start))
	}
	return n
}

// merge inserts iv into the normalized list, coalescing overlapping
// and adjacent intervals.
func merge(ivs []Interval, iv Interval) []Interval {
	out := make([]Interval, 0, len(ivs)+1)
	inserted := false
	for _, cur := range ivs {
		switch {
		case cur.End < iv.Start:
			out = append(out, cur)
		case !inserted && iv.End < cur.Start:
			out = append(out, iv, cur)
			inserted = true
		case inserted:
			out = append(out, cur)
		default:
			iv = Interval{Start: min(iv.Start, cur.Start), End: max(iv.End, cur.End)}
		}
	}
	if !inserted {
		out = append(out, iv)
	}
	return out
}
