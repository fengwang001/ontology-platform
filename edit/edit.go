// Package edit computes a shortest edit script between two line sequences
// using Myers' O(ND) algorithm, with an optional distance cap.
package edit

import (
	"bytes"
	"errors"
)

// ErrTooLarge is returned when the edit distance exceeds the configured cap.
var ErrTooLarge = errors.New("edit: difference too large")

// Op is one step of an edit script. Kind is ' ' (keep), '-' (delete a[A]),
// or '+' (insert b[B]).
type Op struct {
	Kind byte
	A, B int
}

// steps counts diagonal moves (including per-line snake comparisons) of the
// most recent Diff call. Guarded by nothing: Diff is not concurrent-safe.
var steps int

// LastSteps returns the step counter of the most recent Diff call.
func LastSteps() int { return steps }

// Diff returns a shortest script turning a into b. maxDist > 0 caps the
// edit distance; exceeding it returns ErrTooLarge. Ties between shortest
// scripts are broken by preferring deletion over insertion (see DESIGN.md).
func Diff(a, b [][]byte, maxDist int) ([]Op, error) {
	n, m := len(a), len(b)
	maxD := n + m
	if maxDist > 0 && maxDist < maxD {
		maxD = maxDist
	}
	off := maxD
	v := make([]int, 2*maxD+1)
	var trace [][]int
	steps = 0
	d, found := 0, false
	for ; d <= maxD && !found; d++ {
		trace = append(trace, append([]int(nil), v...))
		for k := -d; k <= d; k += 2 {
			steps++
			var x int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1] // insertion (move down)
			} else {
				x = v[off+k-1] + 1 // deletion (move right)
			}
			y := x - k
			for x < n && y < m && bytes.Equal(a[x], b[y]) {
				x, y, steps = x+1, y+1, steps+1
			}
			v[off+k] = x
			if x >= n && y >= m {
				found = true
				break
			}
		}
	}
	if !found {
		return nil, ErrTooLarge
	}
	d--
	ops := make([]Op, 0, n+m)
	x, y := n, m
	for ; d > 0; d-- {
		vp, k := trace[d-1], x-y
		prevK := k - 1
		if k == -d || (k != d && vp[off+k-1] < vp[off+k+1]) {
			prevK = k + 1
		}
		px, py := vp[off+prevK], vp[off+prevK]-prevK
		for x > px && y > py {
			x, y = x-1, y-1
			ops = append(ops, Op{' ', x, y})
		}
		if x == px {
			y--
			ops = append(ops, Op{'+', x, y})
		} else {
			x--
			ops = append(ops, Op{'-', x, y})
		}
	}
	for x > 0 && y > 0 {
		x, y = x-1, y-1
		ops = append(ops, Op{' ', x, y})
	}
	for i, j := 0, len(ops)-1; i < j; i, j = i+1, j-1 {
		ops[i], ops[j] = ops[j], ops[i]
	}
	return ops, nil
}
