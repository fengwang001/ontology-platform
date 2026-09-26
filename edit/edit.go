// Package edit computes a shortest edit script between two line sequences
// using the Myers O(ND) algorithm.
package edit

import (
	"errors"

	"ontology/lines"
)

// Op is one edit operation kind.
type Op int

const (
	// Equal is an unchanged line.
	Equal Op = iota
	// Delete is a line present only in A.
	Delete
	// Insert is a line present only in B.
	Insert
)

// Edit is one aligned line pair. For Equal both lines are set; for Delete
// only A is set; for Insert only B is set.
type Edit struct {
	Op   Op
	A, B lines.Line
}

var (
	// ErrTooDifferent means the edit distance exceeded the configured cap.
	ErrTooDifferent = errors.New("edit: distance exceeds configured maximum")
)

// Engine runs one diff and exposes diagnostic counters.
type Engine struct {
	// MaxDistance, when positive, aborts the search once D would exceed it.
	MaxDistance int
	steps       int
}

type traceSnap struct {
	d int
	v []int
}

// Steps returns the total diagonal moves and snake comparisons made by the
// most recent Diff call.
func (e *Engine) Steps() int { return e.steps }

// Diff returns a shortest edit script from a to b. Ties always prefer a
// deletion (down edge) over an insertion (right edge).
func (e *Engine) Diff(a, b []lines.Line) ([]Edit, error) {
	e.steps = 0
	n, m := len(a), len(b)
	max := n + m
	if e.MaxDistance > 0 && e.MaxDistance < max {
		max = e.MaxDistance
	}
	v := make([]int, 2*max+3)
	off := max + 1
	v[off+1] = 0
	var trace []traceSnap
found:
	for d := 0; d <= max; d++ {
		t := make([]int, len(v))
		copy(t, v)
		trace = append(trace, traceSnap{d, t})
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k-1]
			} else {
				x = v[off+k+1]
			}
			e.steps++
			y := x - k
			for x < n && y < m && lines.Equal(a[x], b[y]) {
				x, y = x+1, y+1
				e.steps++
			}
			v[off+k] = x
			if x >= n && y >= m {
				break found
			}
		}
	}
	if v[off+(m-n)] < n || v[off+(m-n)]-(m-n) < m {
		return nil, ErrTooDifferent
	}
	return backtrack(a, b, trace, off, e), nil
}

func backtrack(a, b []lines.Line, tr []traceSnap, off int, e *Engine) []Edit {
	x, y := len(a), len(b)
	var es []Edit
	_ = e
	for d := len(tr) - 1; d > 0; d-- {
		v := tr[d].v
		k := x - y
		var pk int
		if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
			pk = k - 1
		} else {
			pk = k + 1
		}
		px := v[off+pk]
		for x > px {
			es = append(es, Edit{Op: Equal, A: a[x-1], B: b[y-1]})
			x, y = x-1, y-1
		}
		if d > 0 {
			if x == px {
				es = append(es, Edit{Op: Insert, B: b[y-1]})
				y--
			} else {
				es = append(es, Edit{Op: Delete, A: a[x-1]})
				x--
			}
		}
	}
	for x > 0 {
		es = append(es, Edit{Op: Equal, A: a[x-1], B: b[y-1]})
		x, y = x-1, y-1
	}
	rev(es)
	return es
}

func rev(s []Edit) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}
