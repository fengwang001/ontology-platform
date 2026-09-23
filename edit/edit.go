// Package edit computes a shortest edit script (Myers O(ND)) between two
// line sequences. Ties are broken in favor of deletion, so within a change
// all deletions precede all insertions (see DESIGN.md).
package edit

import (
	"errors"

	"ontology/lines"
)

// ErrTooLarge is returned when the edit distance exceeds Differ.MaxDist.
var ErrTooLarge = errors.New("edit: difference too large")

// OpKind classifies a script operation.
type OpKind int

const (
	Equal OpKind = iota
	Del
	Ins
)

// Op is one script step; Line carries the a-line for Equal/Del and the
// b-line for Ins.
type Op struct {
	Kind OpKind
	Line lines.Line
}

// Differ computes scripts and records the number of diagonal comparisons
// (including per-line snake comparisons) of the most recent Diff.
type Differ struct {
	MaxDist int // 0 means unlimited
	steps   int
}

// Steps returns the comparison counter of the most recent Diff.
func (d *Differ) Steps() int { return d.steps }

// Diff returns a shortest script turning a into b.
func (d *Differ) Diff(a, b []lines.Line) ([]Op, error) {
	d.steps = 0
	n, m := len(a), len(b)
	if n == 0 && m == 0 {
		return nil, nil
	}
	max := n + m
	delta := n - m
	off := max
	v := make([]int, 2*max+1)
	var trace [][]int
	found := -1
	for depth := 0; depth <= max; depth++ {
		if d.MaxDist > 0 && depth > d.MaxDist {
			return nil, ErrTooLarge
		}
		for k := -depth; k <= depth; k += 2 {
			var x int
			if k == -depth || (k != depth && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1] // insertion (down)
			} else {
				x = v[off+k-1] + 1 // deletion (right), wins ties
			}
			y := x - k
			for x < n && y < m {
				d.steps++
				if a[x] != b[y] {
					break
				}
				x++
				y++
			}
			v[off+k] = x
		}
		snap := make([]int, 2*depth+1)
		copy(snap, v[off-depth:off+depth+1])
		trace = append(trace, snap)
		if delta >= -depth && delta <= depth && v[off+delta] >= n {
			found = depth
			break
		}
	}
	if found < 0 {
		return nil, ErrTooLarge
	}
	return d.backtrack(trace, found, a, b, n, m), nil
}

func (d *Differ) backtrack(trace [][]int, dist int, a, b []lines.Line, n, m int) []Op {
	var rev []Op
	x, y := n, m
	for depth := dist; depth >= 1; depth-- {
		vp := trace[depth-1]
		k := x - y
		var pk int
		if k == -depth || (k != depth && vp[k-1+depth-1] < vp[k+1+depth-1]) {
			pk = k + 1 // came from an insertion
		} else {
			pk = k - 1 // came from a deletion
		}
		px, py := vp[pk+depth-1], vp[pk+depth-1]-pk
		ex, ey := px+1, py
		if pk == k+1 {
			ex, ey = px, py+1
		}
		for x > ex && y > ey {
			x--
			y--
			rev = append(rev, Op{Equal, a[x]})
		}
		if pk == k+1 {
			y--
			rev = append(rev, Op{Ins, b[y]})
		} else {
			x--
			rev = append(rev, Op{Del, a[x]})
		}
	}
	for x > 0 && y > 0 {
		x--
		y--
		rev = append(rev, Op{Equal, a[x]})
	}
	out := make([]Op, len(rev))
	for i, op := range rev {
		out[len(rev)-1-i] = op
	}
	return out
}
