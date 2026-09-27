// Package edit computes a shortest edit script between two line sequences
// using the Myers O(ND) algorithm.
package edit

import (
	"errors"

	"ontology/lines"
)

// Kind of an edit operation.
const (
	Equal     byte = ' ' // line present in both files
	Delete    byte = '-' // line only in the old file
	Insert    byte = '+' // line only in the new file
)

// Op is one step of a shortest edit script. Indices are 0-based positions in
// the respective sequence (-1 when unused).
type Op struct {
	Kind byte
	Ai   int
	Bi   int
	Line lines.Line
}

// ErrTooDifferent is returned when the edit distance exceeds MaxD.
var ErrTooDifferent = errors.New("edit: files differ by more than the allowed edit distance")

// TooDifferent reports whether err is ErrTooDifferent.
func TooDifferent(err error) bool { return errors.Is(err, ErrTooDifferent) }

// Differ runs Myers with a configured distance cap. The zero value uses the
// sequence lengths as the cap.
type Differ struct {
	MaxD  int
	steps int64
}

// Steps returns the furrow advances counted by the most recent Diff, including
// every per-line snake comparison.
func (d *Differ) Steps() int64 { return d.steps }

// Diff returns a shortest script from a to b. Tie-breaking is fixed: at equal
// reach the delete (horizontal) edge is preferred, so deletes precede inserts.
func (d *Differ) Diff(a, b []lines.Line) ([]Op, error) {
	n, m := len(a), len(b)
	maxD := d.MaxD
	if maxD <= 0 {
		maxD = n + m
	}
	d.steps = 0
	v := map[int]int{1: 0}
	trace := []map[int]int{}
	for dd := 0; dd <= maxD; dd++ {
		snap := make(map[int]int, len(v))
		for k, x := range v {
			snap[k] = x
		}
		trace = append(trace, snap)
		for k := -dd; k <= dd; k += 2 {
			var x int
			down := k == -dd || (k != dd && v[k-1] < v[k+1])
			if down {
				x = v[k+1]
			} else {
				x = v[k-1] + 1
			}
			y := x - k
			d.steps++ // horizontal or vertical furrow advance
			for x < n && y < m && lines.Equal(a[x], b[y]) {
				d.steps++ // one snake comparison
				x, y = x+1, y+1
			}
			v[k] = x
			if x >= n && y >= m {
				return backtrack(a, b, trace), nil
			}
		}
	}
	return nil, ErrTooDifferent
}

func backtrack(a, b []lines.Line, trace []map[int]int) []Op {
	x, y := len(a), len(b)
	ops := []Op{}
	for dd := len(trace) - 1; dd >= 0; dd-- {
		v := trace[dd]
		k := x - y
		down := k == -dd || (k != dd && v[k-1] < v[k+1])
		var pk, px, py int
		if down {
			pk = k + 1
		} else {
			pk = k - 1
		}
		px = v[pk]
		py = px - pk
		for x > px && y > py {
			ops = append(ops, Op{Equal, x - 1, y - 1, a[x-1]})
			x, y = x-1, y-1
		}
		if dd > 0 {
			if down {
				ops = append(ops, Op{Delete, x - 1, -1, a[x-1]})
			} else {
				ops = append(ops, Op{Insert, -1, y - 1, b[y-1]})
			}
		}
		x, y = px, py
	}
	for i, j := 0, len(ops)-1; i < j; i, j = i+1, j-1 {
		ops[i], ops[j] = ops[j], ops[i]
	}
	return ops
}
