// Package rot generates cyclic rotations, sorts them lexicographically,
// and derives the BWT last column and primary row (forward direction).
package rot

import (
	"bytes"
	"errors"
	"sort"
)

// ErrTerminatorInInput is returned when term already occurs in s.
var ErrTerminatorInInput = errors.New("rot: terminator appears in input")

// Transform computes the BWT of s with terminator term: the last column
// of the sorted rotation matrix of t=s+[term], and the primary row (the
// row holding t itself). Fails wholesale with ErrTerminatorInInput if
// term already occurs in s; no partial result is returned.
func Transform(s []byte, term byte) (last []byte, primary int, err error) {
	if Count(s, term) != 0 {
		return nil, 0, ErrTerminatorInInput
	}
	t := make([]byte, len(s)+1)
	copy(t, s)
	t[len(s)] = term
	last, primary = LastColumn(t)
	return last, primary, nil
}

// LastColumn sorts all len(t) cyclic rotations of t lexicographically
// and returns the last character of each sorted row plus the row index
// of t itself. This is the naive reference, implemented directly.
func LastColumn(t []byte) (last []byte, primary int) {
	n := len(t)
	rows := make([][]byte, n)
	for i := 0; i < n; i++ {
		r := make([]byte, n)
		copy(r, t[i:])
		copy(r[n-i:], t[:i])
		rows[i] = r
	}
	sort.Slice(rows, func(a, b int) bool {
		return bytes.Compare(rows[a], rows[b]) < 0
	})
	last = make([]byte, n)
	orig := string(t)
	for i, r := range rows {
		last[i] = r[n-1]
		if string(r) == orig {
			primary = i
		}
	}
	return last, primary
}

// Sorted returns a sorted copy of b.
func Sorted(b []byte) []byte {
	out := make([]byte, len(b))
	copy(out, b)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Count returns the number of occurrences of c in b.
func Count(b []byte, c byte) int {
	return bytes.Count(b, []byte{c})
}
