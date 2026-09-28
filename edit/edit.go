// Package edit computes a shortest edit script between two line sequences
// using the Myers O(ND) algorithm.
package edit

import (
	"errors"

	"ontology/lines"
)

// Op kinds.
const (
	Equal  = 0 // context line
	Delete = 1 // only in a
	Insert = 2 // only in b
)

// Op is one edit-script step.
type Op struct {
	Kind int
	A    lines.Line // Equal/Delete: line from a
	B    lines.Line // Equal/Insert: line from b
}

// ErrTooDifferent is returned when the edit distance exceeds the configured cap.
var ErrTooDifferent = errors.New("edit: distance exceeds max")

// Differ holds reusable state and the last-run step counter.
type Differ struct {
	steps int
}

// Steps reports diagonal-advance steps (including per-line snake comparisons
// and outer scan steps) consumed by the most recent Diff call.
func (d *Differ) Steps() int { return d.steps }

func eq(x, y lines.Line) bool { return x == y }

// Diff returns a shortest edit script from a to b. maxD caps the edit distance;
// pass maxD < 0 for no cap. Ties resolve delete-first (see DESIGN.md §3).
func (d *Differ) Diff(a, b []lines.Line, maxD int) ([]Op, error) {
	d.steps = 0
	n, m := len(a), len(b)
	v := map[int]int{1: 0}
	trace := []map[int]int{v}
	stop := n + m
	if maxD >= 0 && maxD < stop {
		stop = maxD
	}
	found := false
	var dd int
	for dd = 0; dd <= stop; dd++ {
		d.steps++
		for k := -dd; k <= dd; k += 2 {
			d.steps++
			var x int
			if k == -dd || (k != dd && v[k-1] < v[k+1]) {
				x = v[k+1] // insert
			} else {
				x = v[k-1] + 1 // delete (preferred on ties)
			}
			y := x - k
			for x < n && y < m && eq(a[x], b[y]) {
				d.steps++
				x, y = x+1, y+1
			}
			v[k] = x
			if x >= n && y >= m {
				found = true
				break
			}
		}
		if found {
			break
		}
		nv := make(map[int]int, len(v))
		for k, val := range v {
			nv[k] = val
		}
		trace = append(trace, nv)
		v = nv
	}
	if !found {
		return nil, ErrTooDifferent
	}
	return backtrack(a, b, trace, dd), nil
}

func backtrack(a, b []lines.Line, trace []map[int]int, dd int) []Op {
	x, y := len(a), len(b)
	var ops []Op
	for d := dd; d > 0; d-- {
		v := trace[d-1]
		k := x - y
		var pk int
		if k == -d || (k != d && v[k-1] < v[k+1]) {
			pk = k + 1
		} else {
			pk = k - 1
		}
		px := v[pk]
		py := px - pk
		for x > px && y > py {
			ops = append(ops, Op{Kind: Equal, A: a[x-1], B: b[y-1]})
			x, y = x-1, y-1
		}
		if d > 0 {
			if x == px {
				ops = append(ops, Op{Kind: Insert, B: b[y-1]})
				y--
			} else {
				ops = append(ops, Op{Kind: Delete, A: a[x-1]})
				x--
			}
		}
	}
	for x > 0 {
		ops = append(ops, Op{Kind: Equal, A: a[x-1], B: b[y-1]})
		x, y = x-1, y-1
	}
	for i, j := 0, len(ops)-1; i < j; i, j = i+1, j-1 {
		ops[i], ops[j] = ops[j], ops[i]
	}
	return ops
}
