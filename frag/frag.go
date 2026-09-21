// Package frag tracks the received byte intervals of a single message.
//
// A Set stores fragments as a sorted list of disjoint, non-adjacent
// segments. It supports idempotent duplicate inserts, exact conflict
// detection on overlapping bytes, and completeness checks against the
// total message length. It has no dependencies on other packages.
package frag

import (
	"fmt"
	"sort"
)

// Interval is a half-open byte range [Start, End).
type Interval struct {
	Start, End int64
}

// ConflictError reports a byte mismatch between a new fragment and
// previously stored data. Start and End delimit the exact half-open
// range of differing bytes.
type ConflictError struct {
	Start, End int64
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("frag: conflicting bytes in [%d,%d)", e.Start, e.End)
}

type segment struct {
	start int64
	data  []byte
}

func (s segment) end() int64 { return s.start + int64(len(s.data)) }

// Set is the collection of received fragments for one message.
type Set struct {
	segs  []segment // sorted by start, disjoint, non-adjacent
	bytes int64
}

// Received returns the number of distinct bytes currently stored.
func (s *Set) Received() int64 { return s.bytes }

// Intervals returns the normalized received intervals: ascending,
// non-overlapping, and non-adjacent.
func (s *Set) Intervals() []Interval {
	out := make([]Interval, len(s.segs))
	for i, seg := range s.segs {
		out[i] = Interval{Start: seg.start, End: seg.end()}
	}
	return out
}

// Complete reports whether the stored bytes exactly cover [0, total).
func (s *Set) Complete(total int64) bool {
	return len(s.segs) == 1 && s.segs[0].start == 0 && s.segs[0].end() == total
}

// Assemble returns a copy of the full message. It is only meaningful
// when Complete reports true; otherwise it returns nil.
func (s *Set) Assemble() []byte {
	if len(s.segs) != 1 {
		return nil
	}
	out := make([]byte, len(s.segs[0].data))
	copy(out, s.segs[0].data)
	return out
}

// involved returns the index range [first, last) of segments that
// overlap or touch [lo, hi). When no segment is involved, first == last
// and first is the correct insertion position.
func (s *Set) involved(lo, hi int64) (first, last int) {
	first = sort.Search(len(s.segs), func(i int) bool { return s.segs[i].end() >= lo })
	last = first
	for last < len(s.segs) && s.segs[last].start <= hi {
		last++
	}
	return first, last
}

// Plan reports how many new bytes Add(off, data) would store, without
// mutating the Set. A result of 0 means the insert is an idempotent
// duplicate. It returns a *ConflictError if the overlapping bytes differ.
func (s *Set) Plan(off int64, data []byte) (added int64, err error) {
	lo, hi := off, off+int64(len(data))
	first, last := s.involved(lo, hi)
	newLo, newHi := lo, hi
	var covered int64
	for i := first; i < last; i++ {
		seg := s.segs[i]
		if seg.start < newLo {
			newLo = seg.start
		}
		if seg.end() > newHi {
			newHi = seg.end()
		}
		if err := checkOverlap(seg, lo, hi, off, data); err != nil {
			return 0, err
		}
		covered += int64(len(seg.data))
	}
	return (newHi - newLo) - covered, nil
}

// Add inserts the fragment at [off, off+len(data)), merging overlapping
// and adjacent segments. It returns the number of newly stored bytes
// (0 for an idempotent duplicate). On conflict the Set is left
// untouched and a *ConflictError is returned.
func (s *Set) Add(off int64, data []byte) (added int64, err error) {
	added, err = s.Plan(off, data)
	if err != nil {
		return 0, err
	}
	lo, hi := off, off+int64(len(data))
	first, last := s.involved(lo, hi)
	newLo, newHi := lo, hi
	for i := first; i < last; i++ {
		if s.segs[i].start < newLo {
			newLo = s.segs[i].start
		}
		if s.segs[i].end() > newHi {
			newHi = s.segs[i].end()
		}
	}
	merged := make([]byte, newHi-newLo)
	for i := first; i < last; i++ {
		copy(merged[s.segs[i].start-newLo:], s.segs[i].data)
	}
	copy(merged[off-newLo:], data)
	tail := append([]segment(nil), s.segs[last:]...)
	s.segs = append(s.segs[:first], segment{start: newLo, data: merged})
	s.segs = append(s.segs, tail...)
	s.bytes += added
	return added, nil
}

// checkOverlap compares the overlapping region between seg and the new
// fragment, returning a *ConflictError spanning exactly the differing
// bytes, or nil if the overlap agrees byte for byte.
func checkOverlap(seg segment, lo, hi, off int64, data []byte) error {
	olo := max(lo, seg.start)
	ohi := min(hi, seg.end())
	if olo >= ohi {
		return nil
	}
	first, last := int64(-1), int64(-1)
	for j := olo; j < ohi; j++ {
		if data[j-off] != seg.data[j-seg.start] {
			if first < 0 {
				first = j
			}
			last = j
		}
	}
	if first < 0 {
		return nil
	}
	return &ConflictError{Start: first, End: last + 1}
}
