// Package edit computes shortest edit scripts between two line
// sequences with the Myers O(ND) algorithm.
package edit

import (
	"bytes"
	"errors"
)

// ErrTooBig is returned when the edit distance exceeds the configured
// cap (the "差异过大" class).
var ErrTooBig = errors.New("edit: edit distance exceeds limit")

// Op is one script step: Kind ' ' keeps Line, '-' deletes it from a,
// '+' inserts it from b.
type Op struct {
	Kind byte
	Line []byte
}

// steps counts diagonal advances of the most recent Diff, including each
// per-line snake comparison.
var steps int

// LastSteps returns the counter of the most recent Diff invocation.
func LastSteps() int { return steps }

// Diff returns a shortest script turning a into b. When several shortest
// scripts exist, deletions are preferred over insertions (see DESIGN.md).
// A positive max caps the edit distance; exceeding it returns ErrTooBig.
func Diff(a, b [][]byte, max int) ([]Op, error) {
	n, m := len(a), len(b)
	steps = 0
	lim := n + m
	if max > 0 && max < lim {
		lim = max
	}
	off := lim + 1
	v := make([]int, 2*lim+3)
	var trace [][]int
	done := -1
	for d := 0; d <= lim && done < 0; d++ {
		for k := -d; k <= d; k += 2 {
			steps++
			var x int
			if k == -d || (k != d && v[k-1+off] < v[k+1+off]) {
				x = v[k+1+off] // down: insertion
			} else {
				x = v[k-1+off] + 1 // right: deletion, preferred on ties
			}
			y := x - k
			for x < n && y < m && bytes.Equal(a[x], b[y]) {
				x, y, steps = x+1, y+1, steps+1
			}
			v[k+off] = x
			if x >= n && y >= m {
				done = d
				break
			}
		}
		c := make([]int, 2*d+1)
		copy(c, v[off-d:off+d+1])
		trace = append(trace, c)
	}
	if done < 0 {
		return nil, ErrTooBig
	}
	return backtrack(a, b, trace, done), nil
}

// backtrack rebuilds the script from saved diagonal frontier snapshots.
func backtrack(a, b [][]byte, trace [][]int, D int) []Op {
	var rev []Op
	x, y := len(a), len(b)
	for d := D; d > 0; d-- {
		v, w := trace[d-1], d-1
		k := x - y
		prevK := k - 1
		if k == -d || (k != d && v[k-1+w] < v[k+1+w]) {
			prevK = k + 1
		}
		px, py := v[prevK+w], v[prevK+w]-prevK
		for x > px && y > py {
			x, y = x-1, y-1
			rev = append(rev, Op{' ', a[x]})
		}
		if prevK == k+1 {
			y--
			rev = append(rev, Op{'+', b[y]})
		} else {
			x--
			rev = append(rev, Op{'-', a[x]})
		}
	}
	for x > 0 {
		x--
		rev = append(rev, Op{' ', a[x]})
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}
