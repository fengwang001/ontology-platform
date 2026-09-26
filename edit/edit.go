// Package edit computes shortest edit scripts between two line
// sequences using Myers' O(ND) algorithm, with a configurable
// edit-distance cap and a diagonal-step counter.
package edit

import (
	"errors"
	"sync/atomic"

	"ontology/lines"
)

// ErrTooLarge reports an edit distance above the configured cap.
var ErrTooLarge = errors.New("edit: distance too large")

// Op is one step of an edit script. Kind is ' ' (keep), '-' (delete
// from a) or '+' (insert from b); Line holds the affected line.
type Op struct {
	Kind byte
	Line []byte
}

var steps atomic.Int64

// Steps returns the number of diagonal advances (including per-line
// snake comparisons) performed by the most recent Diff call.
func Steps() int64 { return steps.Load() }

// DiffBytes splits a and b into lines and diffs the line sequences.
func DiffBytes(a, b []byte, max int) ([]Op, error) {
	return Diff(lines.Split(a), lines.Split(b), max)
}

// Diff returns a shortest script turning a into b. max caps the edit
// distance (max < 0 means unlimited); exceeding it yields ErrTooLarge.
// Ties are broken in favour of deletion, so '-' ops precede '+' ops
// inside a change block; the result is deterministic.
func Diff(a, b [][]byte, max int) ([]Op, error) {
	n, m := len(a), len(b)
	steps.Store(0)
	dmax := n + m
	if max >= 0 && max < dmax {
		dmax = max
	}
	off := dmax
	v := make([]int, 2*dmax+1)
	var trace [][]int
	found := -1
	for d := 0; d <= dmax && found < 0; d++ {
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[k-1+off] < v[k+1+off]) {
				x = v[k+1+off] // down: insertion
			} else {
				x = v[k-1+off] + 1 // right: deletion (wins ties)
			}
			y := x - k
			steps.Add(1)
			for x < n && y < m && string(a[x]) == string(b[y]) {
				x, y = x+1, y+1
				steps.Add(1)
			}
			v[k+off] = x
			if x >= n && y >= m {
				found = d
				break
			}
		}
		snap := make([]int, len(v))
		copy(snap, v)
		trace = append(trace, snap)
	}
	if found < 0 {
		return nil, ErrTooLarge
	}
	return backtrack(a, b, trace, found, off), nil
}

// backtrack replays the stored frontiers to emit the script in
// reverse, then flips it into forward order.
func backtrack(a, b [][]byte, trace [][]int, d, off int) []Op {
	x, y := len(a), len(b)
	var rev []Op
	for ; d > 0; d-- {
		v := trace[d-1]
		k := x - y
		pk := k + 1
		if !(k == -d || (k != d && v[k-1+off] < v[k+1+off])) {
			pk = k - 1
		}
		px, py := v[pk+off], v[pk+off]-pk
		for x > px && y > py {
			rev = append(rev, Op{' ', a[x-1]})
			x, y = x-1, y-1
		}
		if x == px {
			rev = append(rev, Op{'+', b[py]})
			y--
		} else {
			rev = append(rev, Op{'-', a[px]})
			x--
		}
	}
	for x > 0 && y > 0 {
		rev = append(rev, Op{' ', a[x-1]})
		x, y = x-1, y-1
	}
	ops := make([]Op, len(rev))
	for i, o := range rev {
		ops[len(rev)-1-i] = o
	}
	return ops
}
