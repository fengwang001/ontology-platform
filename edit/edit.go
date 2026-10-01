// Package edit computes a shortest edit script between two line
// sequences using Myers' O(ND) algorithm with a configurable
// edit-distance limit. Ties between shortest paths are broken by
// preferring deletion over insertion (see DESIGN.md).
package edit

import (
	"bytes"
	"errors"
)

// ErrTooLarge is returned when the edit distance exceeds the limit.
var ErrTooLarge = errors.New("edit: edit distance exceeds limit")

// Op is a single step of an edit script. Kind is ' ' (keep), '-'
// (delete a[A]) or '+' (insert b[B]).
type Op struct {
	Kind byte
	A, B int
}

// Script is an ordered list of ops transforming a into b.
type Script []Op

// work counts diagonal steps (including per-line snake comparisons)
// of the most recent Diff call.
var work int64

// LastWork returns the step counter of the most recent Diff call.
func LastWork() int64 { return work }

// Diff returns a shortest script turning a into b. maxDist <= 0 means
// no limit; if the distance exceeds maxDist, ErrTooLarge is returned.
func Diff(a, b [][]byte, maxDist int) (Script, error) {
	n, m := len(a), len(b)
	limit := maxDist
	if limit <= 0 || limit > n+m {
		limit = n + m
	}
	off := limit + 1
	v := make([]int, 2*limit+3)
	var trace [][]int
	var steps int64
	d, found := 0, false
	for ; d <= limit && !found; d++ {
		for k := -d; k <= d; k += 2 {
			steps++
			var x int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1] // down: insertion
			} else {
				x = v[off+k-1] + 1 // right: deletion (preferred on ties)
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
		trace = append(trace, append([]int(nil), v...))
	}
	work = steps
	if !found {
		return nil, ErrTooLarge
	}
	var ops []Op
	x, y := n, m
	for dd := d; dd > 0; dd-- {
		vp := trace[dd-1]
		k := x - y
		down := k == -dd || (k != dd && vp[off+k-1] < vp[off+k+1])
		pk := k - 1
		if down {
			pk = k + 1
		}
		px, py := vp[off+pk], vp[off+pk]-pk
		ax, ay := px+1, py
		if down {
			ax, ay = px, py+1
		}
		for x > ax && y > ay {
			x, y = x-1, y-1
			ops = append(ops, Op{' ', x, y})
		}
		if down {
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
