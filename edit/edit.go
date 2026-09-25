// Package edit computes a shortest edit script between two line sequences
// using Myers' O(ND) algorithm. Ties are broken in favor of deletion, so
// within a change run all deletions precede insertions (see DESIGN.md).
package edit

import (
	"bytes"
	"errors"

	"ontology/lines"
)

// ErrTooLarge is returned when the edit distance exceeds the caller's limit.
var ErrTooLarge = errors.New("edit: edit distance exceeds limit")

// Op is one step of an edit script. Old is the index into the old sequence
// (for '+' the insertion point), New the index into the new sequence
// (for '-' the deletion point).
type Op struct {
	Kind byte // ' ' keep, '-' delete, '+' insert
	Old  int
	New  int
}

// steps counts diagonal advances (including snake comparisons) of the
// most recent Diff call. Unexported per design; read via Steps.
var steps int

// Steps returns the step counter of the most recent Diff call.
func Steps() int { return steps }

// Diff returns a shortest script turning a into b. maxDist limits the edit
// distance; negative means unlimited. Returns ErrTooLarge when exceeded.
func Diff(a, b []lines.Line, maxDist int) ([]Op, error) {
	n, m := len(a), len(b)
	steps = 0
	lim := n + m
	if maxDist >= 0 && maxDist < lim {
		lim = maxDist
	}
	off := lim + 1
	v := make([]int, 2*lim+3)
	var trace [][]int
	found := -1
	for d := 0; d <= lim && found < 0; d++ {
		for k := -d; k <= d; k += 2 {
			steps++
			var x int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1] // down: insertion
			} else {
				x = v[off+k-1] + 1 // right: deletion (wins ties)
			}
			y := x - k
			for x < n && y < m && bytes.Equal(a[x], b[y]) {
				x, y, steps = x+1, y+1, steps+1
			}
			v[off+k] = x
			if x >= n && y >= m {
				found = d
				break
			}
		}
		if found < 0 {
			cp := make([]int, len(v))
			copy(cp, v)
			trace = append(trace, cp)
		}
	}
	if found < 0 {
		return nil, ErrTooLarge
	}
	var ops []Op
	x, y := n, m
	for d := found; d > 0; d-- {
		vp := trace[d-1]
		k := x - y
		pk := k - 1
		if k == -d || (k != d && vp[off+k-1] < vp[off+k+1]) {
			pk = k + 1
		}
		px, py := vp[off+pk], vp[off+pk]-pk
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
