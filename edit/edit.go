// Package edit computes a shortest edit script between two line sequences
// with the Myers O(ND) algorithm. Ties are resolved in favour of deletions.
package edit

import (
	"errors"

	"ontology/lines"
)

// Kind selects an edit operation.
type Kind uint8

const (
	Equal  Kind = iota // context line
	Delete             // line present only in A
	Insert             // line present only in B
)

// Op is one script operation. For Equal/Delete LineA is the A line;
// for Insert LineB is the B line.
type Op struct {
	Kind Kind
	A    lines.Line
	B    lines.Line
}

// ErrTooDifferent reports that the distance exceeded MaxD.
var ErrTooDifferent = errors.New("edit: difference exceeds configured limit")

// Options configures Diff. MaxD<=0 means unbounded.
type Options struct {
	MaxD int
}

// Differ runs scripts and exposes the diagonal-step counter of the last run.
type Differ struct {
	// Steps counts line comparisons while extending snakes in the last Diff.
	Steps int
}

// Diff returns a shortest script transforming a into b. The delete+insert
// count equals len(a)+len(b)-2*LCS. Ties prefer Delete (see DESIGN.md 3).
func (d *Differ) Diff(a, b []lines.Line, opts Options) ([]Op, error) {
	n, m := len(a), len(b)
	d.Steps = 0
	max := n + m
	if opts.MaxD > 0 && opts.MaxD < max {
		max = opts.MaxD
	}
	v := map[int]int{1: 0}
	var trace []map[int]int
	for D := 0; D <= max; D++ {
		snap := make(map[int]int, len(v))
		for k, x := range v {
			snap[k] = x
		}
		trace = append(trace, snap)
		found := false
		for k := D; k >= -D; k -= 2 {
			var x int
			if k == -D || (k != D && v[k-1] < v[k+1]) {
				x = v[k+1] // move right (insert)
			} else {
				x = v[k-1] + 1 // move down (delete)
			}
			y := x - k
			for x < n && y < m && lines.Equal(a[x], b[y]) {
				d.Steps++
				x, y = x+1, y+1
			}
			if x < n && y < m {
				d.Steps++ // the comparison that ended the snake
			}
			v[k] = x
			if x >= n && y >= m {
				found = true
			}
		}
		if found {
			return backtrack(a, b, trace), nil
		}
	}
	return nil, ErrTooDifferent
}

func backtrack(a, b []lines.Line, trace []map[int]int) []Op {
	x, y := len(a), len(b)
	var ops []Op
	for D := len(trace) - 1; D >= 0; D-- {
		v := trace[D]
		if D == 0 {
			for x > 0 && y > 0 {
				ops = append(ops, Op{Kind: Equal, A: a[x-1], B: b[y-1]})
				x, y = x-1, y-1
			}
			break
		}
		k := x - y
		var prevK int
		if k == -D || (k != D && v[k-1] < v[k+1]) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevX, prevY := v[prevK], v[prevK]-prevK
		for x > prevX && y > prevY {
			ops = append(ops, Op{Kind: Equal, A: a[x-1], B: b[y-1]})
			x, y = x-1, y-1
		}
		x, y = prevX, prevY
		if k == prevK+1 {
			ops = append(ops, Op{Kind: Delete, A: a[x]})
		} else {
			ops = append(ops, Op{Kind: Insert, B: b[y]})
		}
	}
	for i, j := 0, len(ops)-1; i < j; i, j = i+1, j-1 {
		ops[i], ops[j] = ops[j], ops[i]
	}
	return ops
}
