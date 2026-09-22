// Package frag tracks the received byte intervals of a single fragmented
// message and decides overlaps, conflicts and completeness.
//
// A Set stores the assembled bytes plus a normalized interval list: sorted,
// non-overlapping and non-adjacent. It is not safe for concurrent use;
// callers must synchronize externally.
package frag

// Interval is a half-open byte range [Start, End) of received data.
type Interval struct {
	Start int
	End   int
}

// Set is the fragment collection of one message of a known total length.
type Set struct {
	total    int
	data     []byte
	ivs      []Interval
	received int
}

// NewSet returns an empty Set for a message of total bytes.
func NewSet(total int) (*Set, error) {
	if total <= 0 {
		return nil, ErrZeroTotal
	}
	return &Set{total: total, data: make([]byte, total)}, nil
}

// Add inserts the fragment [off, off+len(data)) into the set.
//
// It returns the number of newly covered bytes. A byte-identical duplicate
// returns 0 and changes nothing. If the fragment overlaps received bytes with
// different content, Add returns a *ConflictError and the set is untouched.
//
// reserve, when non-nil, is called with the number of new bytes after all
// validation and before any mutation; if it fails, Add returns its error and
// the set is untouched.
func (s *Set) Add(off int, data []byte, reserve func(n int) error) (int, error) {
	if len(data) == 0 {
		return 0, ErrEmptyData
	}
	end := off + len(data)
	if off < 0 || end > s.total {
		return 0, ErrOutOfBounds
	}
	covered := 0
	diffStart, diffEnd := -1, -1
	for _, iv := range s.ivs {
		lo, hi := max(off, iv.Start), min(end, iv.End)
		if lo >= hi {
			continue
		}
		covered += hi - lo
		for i := lo; i < hi; i++ {
			if s.data[i] != data[i-off] {
				if diffStart < 0 {
					diffStart = i
				}
				diffEnd = i + 1
			}
		}
	}
	if diffStart >= 0 {
		return 0, &ConflictError{Start: diffStart, End: diffEnd}
	}
	delta := len(data) - covered
	if delta > 0 && reserve != nil {
		if err := reserve(delta); err != nil {
			return 0, err
		}
	}
	copy(s.data[off:end], data)
	s.merge(off, end)
	s.received += delta
	return delta, nil
}

// merge folds [start, end) into the interval list, coalescing any
// overlapping or adjacent intervals so the list stays normalized.
func (s *Set) merge(start, end int) {
	out := make([]Interval, 0, len(s.ivs)+1)
	for _, iv := range s.ivs {
		switch {
		case iv.End <= start:
			out = append(out, iv)
		case iv.Start >= end:
			out = append(out, iv)
		default:
			start = min(start, iv.Start)
			end = max(end, iv.End)
		}
	}
	idx := 0
	for idx < len(out) && out[idx].End < start {
		idx++
	}
	out = append(out, Interval{})
	copy(out[idx+1:], out[idx:])
	out[idx] = Interval{Start: start, End: end}
	s.ivs = out
}

// Complete reports whether every byte of [0, total) has been received.
func (s *Set) Complete() bool {
	return s.received == s.total
}

// Received reports how many distinct bytes have been received so far.
func (s *Set) Received() int {
	return s.received
}

// Total reports the declared message length.
func (s *Set) Total() int {
	return s.total
}

// Intervals returns a copy of the normalized received-interval list:
// ascending, mutually non-overlapping and non-adjacent.
func (s *Set) Intervals() []Interval {
	out := make([]Interval, len(s.ivs))
	copy(out, s.ivs)
	return out
}

// Bytes returns a copy of the assembled message. It is only meaningful once
// Complete reports true.
func (s *Set) Bytes() []byte {
	out := make([]byte, s.total)
	copy(out, s.data)
	return out
}
