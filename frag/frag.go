package frag

type fragment struct {
	ival Interval
	data []byte
}

// Set tracks the fragments received so far for one message of a known
// total length. It is not safe for concurrent use; callers must
// serialize access.
type Set struct {
	total  int
	ivals  []Interval // normalized: sorted, disjoint, non-adjacent
	frags  []fragment
	stored int // sum of len(frag.data), i.e. bytes held in memory
}

// New returns an empty Set for a message of total bytes.
func New(total int) *Set { return &Set{total: total} }

// Total returns the declared total message length.
func (s *Set) Total() int { return s.total }

// Stored returns the number of bytes currently held in memory.
func (s *Set) Stored() int { return s.stored }

// Received returns the number of distinct message bytes covered so far.
func (s *Set) Received() int {
	n := 0
	for _, iv := range s.ivals {
		n += iv.len()
	}
	return n
}

// Intervals returns a copy of the normalized received-interval list.
func (s *Set) Intervals() []Interval {
	out := make([]Interval, len(s.ivals))
	copy(out, s.ivals)
	return out
}

// Complete reports whether [0, total) is fully covered.
func (s *Set) Complete() bool {
	return len(s.ivals) == 1 && s.ivals[0] == (Interval{0, s.total})
}

// Check reports whether adding data at off would be an exact duplicate of
// already covered bytes (dup) or would conflict with stored bytes (err).
// It never mutates the Set.
func (s *Set) Check(off int, data []byte) (dup bool, err error) {
	n := Interval{off, off + len(data)}
	first, last := -1, -1
	for _, f := range s.frags {
		if !f.ival.overlaps(n) {
			continue
		}
		lo := max(f.ival.Start, n.Start)
		hi := min(f.ival.End, n.End)
		for i := lo; i < hi; i++ {
			if f.data[i-f.ival.Start] != data[i-n.Start] {
				if first == -1 {
					first = i
				}
				last = i
			}
		}
	}
	if first != -1 {
		return false, &ConflictError{Start: first, End: last + 1}
	}
	for _, iv := range s.ivals {
		if iv.Start <= n.Start && n.End <= iv.End {
			return true, nil // fully covered already: idempotent
		}
	}
	return false, nil
}

// Add stores data at off and merges its interval. Callers must have
// validated the fragment (range, conflicts) with Check first.
func (s *Set) Add(off int, data []byte) {
	cp := make([]byte, len(data))
	copy(cp, data)
	n := Interval{off, off + len(data)}
	s.frags = append(s.frags, fragment{ival: n, data: cp})
	s.stored += len(data)
	s.ivals = insertInterval(s.ivals, n)
}

// Assemble concatenates all fragments into the full message. The result
// is only meaningful when Complete reports true.
func (s *Set) Assemble() []byte {
	out := make([]byte, s.total)
	for _, f := range s.frags {
		copy(out[f.ival.Start:f.ival.End], f.data)
	}
	return out
}
