// Package edit computes a shortest edit script between two line
// sequences using Myers' O(ND) greedy algorithm with an optional
// edit-distance limit. Ties are broken in favor of deletion, which
// makes the script deterministic (see DESIGN.md §3).
package edit

import (
	"bytes"
	"errors"
)

// ErrTooLarge is returned when the edit distance exceeds the limit.
var ErrTooLarge = errors.New("edit: difference too large")

// Op is one step of an edit script. Kind is ' ' (keep), '-' (delete
// a[A]) or '+' (insert b[B]). A indexes the old sequence, B the new.
type Op struct {
	Kind byte
	A, B int
}

var lastSteps int

// Steps returns the number of diagonal advances (including per-line
// snake comparisons) performed by the most recent call to Diff.
func Steps() int { return lastSteps }

// Diff returns a shortest edit script turning a into b. maxDist > 0
// caps the edit distance; larger distances yield ErrTooLarge.
func Diff(a, b [][]byte, maxDist int) ([]Op, error) {
	n, m := len(a), len(b)
	limit := n + m
	if maxDist > 0 && maxDist < limit {
		limit = maxDist
	}
	off := limit + 1
	v := make([]int, 2*limit+3)
	var trace [][]int
	steps := 0
	found := false
	d := 0
	for ; d <= limit && !found; d++ {
		cp := make([]int, len(v))
		copy(cp, v)
		trace = append(trace, cp)
		for k := -d; k <= d; k += 2 {
			steps++
			var x int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1] // insertion
			} else {
				x = v[off+k-1] + 1 // deletion wins ties
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
	lastSteps = steps
	if !found {
		return nil, ErrTooLarge
	}
	return backtrack(trace, off, n, m), nil
}

func backtrack(trace [][]int, off, n, m int) []Op {
	var ops []Op
	x, y := n, m
	for d := len(trace) - 1; d > 0; d-- {
		vp := trace[d-1]
		k := x - y
		var pk int
		if k == -d || (k != d && vp[off+k-1] < vp[off+k+1]) {
			pk = k + 1
		} else {
			pk = k - 1
		}
		px, py := vp[off+pk], vp[off+pk]-pk
		for x > px && y > py {
			x, y = x-1, y-1
			ops = append(ops, Op{' ', x, y})
		}
		if x > px {
			x--
			ops = append(ops, Op{'-', x, y})
		} else {
			y--
			ops = append(ops, Op{'+', x, y})
		}
	}
	for x > 0 && y > 0 {
		x, y = x-1, y-1
		ops = append(ops, Op{' ', x, y})
	}
	for i, j := 0, len(ops)-1; i < j; i, j = i+1, j-1 {
		ops[i], ops[j] = ops[j], ops[i]
	}
	return ops
}
