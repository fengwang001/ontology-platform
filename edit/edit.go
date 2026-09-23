// Package edit computes a shortest edit script between two line sequences
// with the Myers O(ND) algorithm and an editable distance cap.
package edit

import (
	"errors"

	"ontology/lines"
)

// Kind enumerates edit operation kinds.
type Kind uint8

const (
	Equal Kind = iota
	Delete
	Insert
)

// Op is one script step: A is the old line (Delete/Equal), B the new (Insert/Equal).
type Op struct {
	Kind Kind
	A, B lines.Line
}

// ErrTooDifferent is returned when the edit distance exceeds MaxDist.
var ErrTooDifferent = errors.New("edit: distance exceeds configured maximum")

// Options controls Diff.
type Options struct{ MaxDist int }

// steps counts diagonal advances including per-line snake comparisons
// of the most recent Diff call.
var steps int64

// Steps returns the counter value (for complexity tests).
func Steps() int64 { return steps }

// Diff returns a shortest script turning a into b, delete-first on ties.
func Diff(a, b []lines.Line, opts Options) ([]Op, error) {
	steps = 0
	n, m := len(a), len(b)
	maxD := opts.MaxDist
	if maxD <= 0 || maxD > n+m {
		maxD = n + m
	}
	size := 2*maxD + 3
	v := make([]int, size)
	snaps := make([][]int, 0, maxD+1)
	for d := 0; ; d++ {
		if d > maxD {
			return nil, ErrTooDifferent
		}
		for k := -d; k <= d; k += 2 {
			off := maxD + 1
			down := v[k-1+off]
			right := v[k+1+off]
			x := right
			if k == d || (k != -d && down >= right) {
				x = down // delete (move down), chosen on tie
			}
			y := x - k
			steps++ // one diagonal advance onto the snake
			for x < n && y < m && lines.Equal(a[x], b[y]) {
				steps++ // one per-line snake comparison (matched)
				x++
				y++
			}
			if x < n && y < m {
				steps++ // the failing comparison that ends the snake
			}
			v[k+off] = x
		}
		snaps = append(snaps, append([]int(nil), v...))
		if reached(v, maxD+1, n, m) {
			break
		}
	}
	return backtrack(a, b, snaps, maxD+1), nil
}

// reached reports whether any tracked diagonal sits at the endpoint.
func reached(v []int, off, n, m int) bool {
	for k := -off + 1; k < off; k++ {
		x := v[k+off]
		if x == n && x-k == m {
			return true
		}
	}
	return false
}

// backtrack rebuilds the shortest script from V-front snapshots, delete-first.
func backtrack(a, b []lines.Line, snaps [][]int, off int) []Op {
	x, y := len(a), len(b)
	var rev []Op
	for d := len(snaps) - 1; d >= 0; d-- {
		v := snaps[d]
		k := x - y
		var pk int
		if k == -d || (k != d && v[k-1+off] >= v[k+1+off]) {
			pk = k - 1 // came from a deletion
		} else {
			pk = k + 1 // came from an insertion
		}
		px, py := v[pk+off], v[pk+off]-pk
		if d == 0 {
			px, py = 0, 0
		}
		for x > px && y > py {
			rev = append(rev, Op{Kind: Equal, A: a[x-1], B: b[y-1]})
			x--
			y--
		}
		if d > 0 {
			if x == px+1 && y == py {
				rev = append(rev, Op{Kind: Delete, A: a[x-1]})
				x--
			} else {
				rev = append(rev, Op{Kind: Insert, B: b[y-1]})
				y--
			}
		}
	}
	for x > 0 || y > 0 {
		if x > 0 && y > 0 {
			rev = append(rev, Op{Kind: Equal, A: a[x-1], B: b[y-1]})
			x--
			y--
		} else if x > 0 {
			rev = append(rev, Op{Kind: Delete, A: a[x-1]})
			x--
		} else {
			rev = append(rev, Op{Kind: Insert, B: b[y-1]})
			y--
		}
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}
