// Package seqno tracks the set of sequence numbers a single replication-group
// member has processed and maintains that member's local checkpoint (lcp):
// the largest k such that 1..k are all processed.
//
// The lcp is advanced incrementally. Instead of rescanning from 1 on every
// insertion, the set remembers the first unfilled position (gap) and walks it
// forward only across positions that actually become contiguous. Therefore the
// total number of forward lcp steps over a member's lifetime never exceeds the
// number of distinct sequence numbers it processed, regardless of arrival
// order: reverse-order acks are as cheap as in-order ones.
package seqno

// Set is a processed-sequence-number set with an incrementally maintained
// local checkpoint. It is not safe for concurrent use; callers must
// synchronize.
type Set struct {
	bits map[int64]struct{}
	gap  int64 // smallest sequence number not known processed (lcp == gap-1)

	// steps counts individual lcp forward advances (one per sequence number
	// that becomes part of the processed prefix). It is a proof instrument:
	// over any sequence without promotions, summed across members it is
	// bounded by the number of accepted first acks plus the number of writes.
	steps int64
}

// New returns an empty set whose lcp is 0.
func New() *Set {
	return &Set{bits: make(map[int64]struct{}), gap: 1}
}

// Set records seq as processed. seq must be positive; non-positive values are
// ignored and return false. It reports whether seq was newly recorded (a
// repeated set is idempotent and returns false).
func (s *Set) Set(seq int64) bool {
	if seq < 1 {
		return false
	}
	if _, ok := s.bits[seq]; ok {
		return false
	}
	s.bits[seq] = struct{}{}
	if seq == s.gap {
		for {
			if _, ok := s.bits[s.gap]; !ok {
				break
			}
			s.gap++
			s.steps++
		}
	}
	return true
}

// Has reports whether seq has been processed.
func (s *Set) Has(seq int64) bool {
	if seq < 1 {
		return false
	}
	_, ok := s.bits[seq]
	return ok
}

// LCP returns the local checkpoint: the largest k with 1..k all processed.
func (s *Set) LCP() int64 { return s.gap - 1 }

// Max returns the greatest processed sequence number, or 0 if none.
func (s *Set) Max() int64 {
	var max int64
	for seq := range s.bits {
		if seq > max {
			max = seq
		}
	}
	return max
}

// Steps returns the total number of one-position lcp advances performed.
func (s *Set) Steps() int64 { return s.steps }

// TruncateAbove forgets every processed sequence number strictly greater than
// keep and recomputes the lcp. Used when a member rolls back to a checkpoint
// during a promotion; it never inflates the step counter.
func (s *Set) TruncateAbove(keep int64) {
	for seq := range s.bits {
		if seq > keep {
			delete(s.bits, seq)
		}
	}
	s.gap = 1
	for {
		if _, ok := s.bits[s.gap]; !ok {
			break
		}
		s.gap++
	}
}
