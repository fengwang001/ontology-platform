// Package edit computes a shortest edit script between two line sequences
// using Myers' O(ND) algorithm. Ties resolve toward deletions (see DESIGN.md).
package edit

import (
	"errors"

	"ontology/lines"
)

// Kind is an edit operation kind.
type Kind uint8

const (
	Equal Kind = iota
	Delete
	Insert
)

// Op is one edit operation over a single line.
type Op struct {
	Kind Kind
	A    lines.Line // old line for Equal/Delete
	B    lines.Line // new line for Equal/Insert
}

// ErrTooDifferent is returned when the edit distance exceeds the configured cap.
var ErrTooDifferent = errors.New("edit: difference exceeds configured limit")

// Engine computes diffs. Steps counts diagonal moves plus per-line snake
// comparisons made by the most recent Diff call (including the final mismatch).
type Engine struct {
	Steps int
}

// Diff returns a shortest edit script from a to b. If maxD >= 0 and the edit
// distance exceeds maxD, it returns ErrTooDifferent.
func (e *Engine) Diff(a, b []lines.Line, maxD int) ([]Op, error) {
	n, m := len(a), len(b)
	e.Steps = 0
	type snap struct{ d, k, x int }
	var trace []map[int]int
	found := false
	d := 0
	for ; d <= maxD || maxD < 0; d++ {
		v := map[int]int{}
		if len(trace) > 0 {
			for k, x := range trace[len(trace)-1] {
				v[k] = x
			}
		}
		for k := -d; k <= d; k += 2 {
			var x int
			switch {
			case k == -d:
				x = v[k+1] // must insert (move right)
			case k == d:
				x = v[k-1] + 1 // prefer delete (move down)
			default:
				if v[k-1] >= v[k+1] {
					x = v[k-1] + 1 // prefer delete on ties
				} else {
					x = v[k+1]
				}
			}
			e.Steps++ // one diagonal advance
			y := x - k
			for x < n && y < m {
				e.Steps++ // one snake comparison
				if string(a[x].Content) != string(b[y].Content) ||
					string(a[x].Term) != string(b[y].Term) {
					break
				}
				x++
				y++
			}
			v[k] = x
			if x >= n && y >= m {
				found = true
				break
			}
		}
		trace = append(trace, v)
		if found {
			break
		}
		if maxD >= 0 && d >= maxD {
			return nil, ErrTooDifferent
		}
	}
	ops := e.backtrack(a, b, trace)
	return ops, nil
}

func (e *Engine) backtrack(a, b []lines.Line, trace []map[int]int) []Op {
	var ops []Op
	x, y := len(a), len(b)
	for d := len(trace) - 1; d >= 0; d-- {
		v := trace[d]
		k := x - y
		var pk int
		switch {
		case k == -d:
			pk = k + 1
		case k == d:
			pk = k - 1
		default:
			if v[k-1] >= v[k+1] {
				pk = k - 1
			} else {
				pk = k + 1
			}
		}
		px := v[pk]
		py := px - pk
		for x > px && y > py {
			ops = append(ops, Op{Kind: Equal, A: a[x-1], B: b[y-1]})
			x--
			y--
		}
		if d > 0 {
			switch {
			case x == px:
				ops = append(ops, Op{Kind: Insert, B: b[y-1]})
				y--
			case y == py:
				ops = append(ops, Op{Kind: Delete, A: a[x-1]})
				x--
			default:
				if pk == k-1 {
					ops = append(ops, Op{Kind: Delete, A: a[x-1]})
					x--
				} else {
					ops = append(ops, Op{Kind: Insert, B: b[y-1]})
					y--
				}
			}
		}
	}
	for i, j := 0, len(ops)-1; i < j; i, j = i+1, j-1 {
		ops[i], ops[j] = ops[j], ops[i]
	}
	return ops
}

// Distance returns the number of delete+insert operations in a script.
func Distance(ops []Op) int {
	d := 0
	for _, op := range ops {
		if op.Kind != Equal {
			d++
		}
	}
	return d
}
