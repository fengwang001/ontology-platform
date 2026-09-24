// Package mono performs a single-pass monotone-stack scan over a sequence.
// It depends only on the stack package.
package mono

import (
	"errors"
	"sync/atomic"

	"ontology/stack"
)

// None is the answer for a position with no satisfying element on the right.
// It is negative, so it can never equal a legal index (0 included).
const None = -1

var (
	// ErrNilInput is returned when the input slice is nil.
	ErrNilInput = errors.New("mono: nil input slice")
	// ErrTooLong is returned when len(input) exceeds the configured maximum.
	ErrTooLong = errors.New("mono: input length exceeds configured maximum")
	// ErrBadLimit is returned when the configured maximum is zero or negative.
	ErrBadLimit = errors.New("mono: maximum length must be positive")
)

// Scanner finds the next strictly greater element to the right of each
// position. A Scanner may serve repeated Scan calls; a rejected call never
// mutates the input and leaves the Scanner fully usable.
type Scanner struct {
	maxLen int
	// ops counts total pushes plus pops. It is intentionally non-exported
	// and never exposed in the public API; atomic keeps concurrent reads
	// race-free.
	ops atomic.Int64
}

// NewScanner builds a Scanner rejecting sequences longer than maxLen.
func NewScanner(maxLen int) (*Scanner, error) {
	if maxLen <= 0 {
		return nil, ErrBadLimit
	}
	return &Scanner{maxLen: maxLen}, nil
}

// Scan returns ans[i] = the smallest j > i with a[j] > a[i] (strictly
// greater; equal values do not satisfy the relation), or None. Equal values
// are kept on the stack, which keeps stack values non-increasing bottom to
// top. On error no partial result is returned.
func (s *Scanner) Scan(a []int) ([]int, error) {
	if a == nil {
		return nil, ErrNilInput
	}
	if len(a) > s.maxLen {
		return nil, ErrTooLong
	}
	ans := make([]int, len(a))
	for i := range ans {
		ans[i] = None
	}
	var st stack.Stack
	for i, v := range a {
		for st.Len() > 0 && a[st.Top()] < v {
			ans[st.Pop()] = i
			s.ops.Add(1)
		}
		st.Push(i)
		s.ops.Add(1)
	}
	return ans, nil
}
