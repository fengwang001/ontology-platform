// Package edit computes a shortest edit script between two line
// sequences using the Myers O(ND) algorithm. Ties between equally
// short scripts are resolved by preferring deletion over insertion,
// so within one change block all deletions precede insertions.
package edit

import (
	"bytes"
	"errors"
)

// ErrTooDifferent is returned when the edit distance exceeds maxDist.
var ErrTooDifferent = errors.New("edit: difference too large")

// Kind classifies one edit operation.
type Kind int

const (
	Keep Kind = iota
	Del
	Ins
)

// Op is one operation of an edit script.
type Op struct {
	Kind Kind
	Text []byte
}

var lastSteps int

// Steps returns the number of diagonal steps (including per-line snake
// comparisons) taken by the most recent Diff call.
func Steps() int { return lastSteps }

// Diff returns a shortest script turning a into b. maxDist >= 0 caps
// the edit distance; exceeding it yields ErrTooDifferent.
func Diff(a, b [][]byte, maxDist int) ([]Op, error) {
	lastSteps = 0
	n, m := len(a), len(b)
	lim := n + m
	if maxDist >= 0 && maxDist < lim {
		lim = maxDist
	}
	off := lim + 1
	v := make([]int, 2*lim+3)
	var trace [][]int
	d := 0
	found := false
	for ; d <= lim && !found; d++ {
		trace = append(trace, append([]int(nil), v...))
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1]
			} else {
				x = v[off+k-1] + 1
			}
			y := x - k
			lastSteps++
			for x < n && y < m && bytes.Equal(a[x], b[y]) {
				x++
				y++
				lastSteps++
			}
			v[off+k] = x
			if x >= n && y >= m {
				found = true
				break
			}
		}
	}
	if !found {
		return nil, ErrTooDifferent
	}
	return backtrack(a, b, trace, d-1, off), nil
}

// backtrack replays the stored V arrays to recover the script.
func backtrack(a, b [][]byte, trace [][]int, d, off int) []Op {
	var rev []Op
	x, y := len(a), len(b)
	for ; d > 0; d-- {
		v := trace[d]
		k := x - y
		prevK := k - 1 // deletion
		if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
			prevK = k + 1 // insertion
		}
		px, py := v[off+prevK], v[off+prevK]-prevK
		ex := px
		if prevK == k-1 {
			ex = px + 1
		}
		for x > ex { // snake
			rev = append(rev, Op{Keep, a[x-1]})
			x--
			y--
		}
		if prevK == k-1 {
			rev = append(rev, Op{Del, a[px]})
		} else {
			rev = append(rev, Op{Ins, b[py]})
		}
		x, y = px, py
	}
	for x > 0 { // leading snake at d=0
		rev = append(rev, Op{Keep, a[x-1]})
		x--
	}
	ops := make([]Op, len(rev))
	for i, op := range rev {
		ops[len(rev)-1-i] = op
	}
	return ops
}
