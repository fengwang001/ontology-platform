// Package seg holds the internal state of a single session: events
// collected by Seq, gap/overflow judgement, ordered fold, close semantics.
package seg

import (
	"errors"
	"sort"
)

// Sentinel errors, mutually distinguishable via errors.Is.
var (
	ErrBadSeq     = errors.New("seg: seq must be >= 1")
	ErrBadValue   = errors.New("seg: value must be in [0,9]")
	ErrConflict   = errors.New("seg: conflicting value for duplicate seq")
	ErrClosed     = errors.New("seg: session already closed")
	ErrIncomplete = errors.New("seg: seq 1..N not exactly present")
)

// Seg is one session's state. Not goroutine-safe; callers serialize.
type Seg struct {
	vals   map[int]int // seq -> value, at most one value per seq
	closed bool
	n      int
	result int
}

// New returns an empty, open session.
func New() *Seg { return &Seg{vals: map[int]int{}} }

// Append records one event. Same seq with same value is idempotent;
// same seq with a different value is a conflict. Any append after close
// is rejected. Rejected appends change nothing.
func (s *Seg) Append(seq, value int) error {
	if s.closed {
		return ErrClosed
	}
	if v, ok := s.vals[seq]; ok {
		if v == value {
			return nil
		}
		return ErrConflict
	}
	s.vals[seq] = value
	return nil
}

// Close freezes the result iff seq 1..n are exactly present (no gap, no
// seq > n). On an already-closed session, the same n is idempotent
// success, a different n is rejected. Failed closes change nothing.
func (s *Seg) Close(n int) error {
	if s.closed {
		if n == s.n {
			return nil
		}
		return ErrClosed
	}
	if n <= 0 {
		return ErrBadSeq
	}
	if len(s.vals) != n {
		return ErrIncomplete
	}
	r := 0
	for i := 1; i <= n; i++ {
		v, ok := s.vals[i]
		if !ok {
			return ErrIncomplete
		}
		r = r*10 + v
	}
	s.closed, s.n, s.result = true, n, r
	return nil
}

// Result returns the frozen result and whether the session is closed.
func (s *Seg) Result() (int, bool) { return s.result, s.closed }

// Seen returns the recorded seq numbers in ascending order.
func (s *Seg) Seen() []int {
	out := make([]int, 0, len(s.vals))
	for k := range s.vals {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}
