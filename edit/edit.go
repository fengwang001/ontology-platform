// Package edit computes a shortest edit script between two line sequences
// using Myers' O(ND) greedy algorithm with a configurable distance limit.
// Ties between equally short scripts are broken by preferring deletion
// over insertion (see DESIGN.md section 3).
package edit

import (
	"bytes"
	"errors"
)

// ErrTooBig is returned when the edit distance exceeds the configured limit.
var ErrTooBig = errors.New("edit: difference too large")

// Kind classifies one step of an edit script.
type Kind byte

const (
	Keep Kind = iota // line present in both sequences
	Del              // line only in the old sequence
	Ins              // line only in the new sequence
)

// Op is one step of an edit script. Line indexes the old sequence for
// Keep/Del and the new sequence for Ins.
type Op struct {
	Kind Kind
	Line int
}

// lastSteps counts diagonal advances (including per-line snake compares)
// of the most recent Diff call. Unexported per spec; read via LastSteps.
var lastSteps int

// LastSteps returns the step counter of the most recent Diff call.
func LastSteps() int { return lastSteps }

// Diff returns a shortest script turning a into b. maxDist >= 0 caps the
// edit distance; exceeding it returns ErrTooBig. A negative maxDist means
// no limit. The result is deterministic: on ties deletion is preferred.
func Diff(a, b [][]byte, maxDist int) ([]Op, error) {
	n, m := len(a), len(b)
	limit := n + m
	if maxDist >= 0 && maxDist < limit {
		limit = maxDist
	}
	lastSteps = 0
	off := limit + 1
	v := make([]int, 2*limit+3)
	var trace [][]int
	found := false
	d := 0
	for ; d <= limit && !found; d++ {
		for k := -d; k <= d; k += 2 {
			lastSteps++
			var x int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1] // down: insertion
			} else {
				x = v[off+k-1] + 1 // right: deletion wins ties
			}
			y := x - k
			for x < n && y < m && bytes.Equal(a[x], b[y]) {
				x, y, lastSteps = x+1, y+1, lastSteps+1
			}
			v[off+k] = x
			if x >= n && y >= m {
				found = true
				break
			}
		}
		trace = append(trace, append([]int(nil), v...))
	}
	if !found {
		return nil, ErrTooBig
	}
	return backtrace(trace, d-1, off, n, m), nil
}

// backtrace walks the saved V snapshots from (n,m) to (0,0), emitting the
// script in reverse and then flipping it.
func backtrace(trace [][]int, depth, off, n, m int) []Op {
	var rev []Op
	x, y := n, m
	for d := depth; d >= 1; d-- {
		v := trace[d-1]
		k := x - y
		ins := k == -d || (k != d && v[off+k-1] < v[off+k+1])
		prevK := k - 1
		if ins {
			prevK = k + 1
		}
		prevX := v[off+prevK]
		prevY := prevX - prevK
		ex, ey := prevX, prevY
		if ins {
			ey++
		} else {
			ex++
		}
		for x > ex && y > ey {
			x, y = x-1, y-1
			rev = append(rev, Op{Keep, x})
		}
		if ins {
			y--
			rev = append(rev, Op{Ins, y})
		} else {
			x--
			rev = append(rev, Op{Del, x})
		}
	}
	for x > 0 && y > 0 {
		x, y = x-1, y-1
		rev = append(rev, Op{Keep, x})
	}
	ops := make([]Op, len(rev))
	for i, op := range rev {
		ops[len(rev)-1-i] = op
	}
	return ops
}
