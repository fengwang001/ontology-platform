// Package edit computes a shortest edit script (Myers O(ND)) between
// two line sequences, with a configurable distance limit. Ties between
// shortest scripts are resolved deletion-first (see DESIGN.md).
package edit

import "errors"

// Op is one edit step; Kind is ' ' (keep), '-' (delete) or '+' (insert).
type Op struct {
	Kind byte
	Line string
}

// ErrTooLarge reports a diff whose distance exceeds the configured limit.
var ErrTooLarge = errors.New("edit: diff too large")

var steps int // diagonal steps (incl. snake comparisons) of the last Diff

// LastSteps returns the step counter of the most recent Diff call.
func LastSteps() int { return steps }

// Diff returns a shortest script turning a into b. maxDist caps the edit
// distance (negative means unlimited); exceeding it yields ErrTooLarge.
func Diff(a, b []string, maxDist int) ([]Op, error) {
	n, m := len(a), len(b)
	steps = 0
	if maxDist < 0 || maxDist > n+m {
		maxDist = n + m
	}
	off := maxDist + 1
	size := 2*maxDist + 3
	v := make([]int, size)
	var trace [][]int
	found := -1
	for d := 0; d <= maxDist && found < 0; d++ {
		for k := -d; k <= d; k += 2 {
			steps++
			var x int
			if k == -d {
				x = v[off+k+1]
			} else if k == d || v[off+k-1]+1 > v[off+k+1] {
				x = v[off+k-1] + 1
			} else {
				x = v[off+k+1]
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x, y, steps = x+1, y+1, steps+1
			}
			v[off+k] = x
			if x >= n && y >= m {
				found = d
				break
			}
		}
		snap := make([]int, size)
		copy(snap, v)
		trace = append(trace, snap)
	}
	if found < 0 {
		return nil, ErrTooLarge
	}
	return backtrack(a, b, trace, found, off), nil
}

func backtrack(a, b []string, trace [][]int, d, off int) []Op {
	var rev []Op
	x, y := len(a), len(b)
	for ; d > 0; d-- {
		prev := trace[d-1]
		k := x - y
		del := k != -d && (k == d || prev[off+k-1]+1 > prev[off+k+1])
		var px, py, mx int
		if del {
			px = prev[off+k-1]
			py = px - (k - 1)
			mx = px + 1
		} else {
			px = prev[off+k+1]
			py = px - (k + 1)
			mx = px
		}
		for x > mx {
			x, y = x-1, y-1
			rev = append(rev, Op{' ', a[x]})
		}
		if del {
			rev = append(rev, Op{'-', a[px]})
		} else {
			rev = append(rev, Op{'+', b[py]})
		}
		x, y = px, py
	}
	for x > 0 {
		x, y = x-1, y-1
		rev = append(rev, Op{' ', a[x]})
	}
	out := make([]Op, len(rev))
	for i, op := range rev {
		out[len(rev)-1-i] = op
	}
	return out
}
