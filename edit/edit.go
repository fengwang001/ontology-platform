// Package edit computes a shortest edit script between two line sequences
// using the Myers O(ND) algorithm. It depends only on package lines.
package edit

import (
	"bytes"
	"errors"

	"ontology/lines"
)

// ErrTooDifferent means the edit distance exceeded MaxDistance.
var ErrTooDifferent = errors.New("edit: files differ by more than the limit")

// Kind classifies one script operation.
type Kind uint8

const (
	Equal Kind = iota
	Delete
	Insert
)

// Op is one edit operation. For Equal/Delete L is an old line; for Insert
// L is a new line.
type Op struct {
	Kind Kind
	L    lines.Line
}

// Counter holds the number of diagonal forward steps (snake comparisons
// included) taken by the most recent Diff.
type Counter struct{ Steps int64 }

// Options controls Diff. MaxDistance<=0 means unlimited.
type Options struct {
	MaxDistance int
	Counter     *Counter
}

// Diff returns a shortest script transforming a into b. Ties are broken in
// favour of deletions (see DESIGN.md section 3).
func Diff(a, b []lines.Line, opt Options) ([]Op, error) {
	n, m := len(a), len(b)
	maxD := n + m
	if opt.MaxDistance > 0 {
		maxD = opt.MaxDistance
	}
	var steps int64
	v := make(map[int]int)
	trace := make([]map[int]int, 0, maxD+1)
	found := false
	for d := 0; d <= maxD; d++ {
		cur := make(map[int]int, 2*d+1)
		for k := d; k >= -d; k -= 2 {
			var x int
			if d == 0 {
				x = 0
			} else if k == d || (k != -d && v[k+1]+1 >= v[k-1]) {
				x = v[k+1] + 1 // down: delete, preferred on ties
			} else {
				x = v[k-1] // right: insert
			}
			y := x - k
			for x >= 0 && y >= 0 && x < n && y < m && bytes.Equal(a[x].Bytes(), b[y].Bytes()) {
				x++
				y++
				steps++
			}
			steps++
			v[k] = x
			cur[k] = x
			if x >= n && y >= m {
				found = true
			}
		}
		trace = append(trace, cur)
		if found {
			break
		}
		if d == maxD {
			if opt.Counter != nil {
				opt.Counter.Steps = steps
			}
			return nil, ErrTooDifferent
		}
	}
	if opt.Counter != nil {
		opt.Counter.Steps = steps
	}
	return backtrack(a, b, trace), nil
}

func backtrack(a, b []lines.Line, trace []map[int]int) []Op {
	var ops []Op
	x, y := len(a), len(b)
	for d := len(trace) - 1; d >= 0; d-- {
		var px, py int
		down := true
		if d > 0 {
		prev := trace[d-1]
		k := x - y
			down = k == d || (k != -d && get(prev, k+1)+1 >= get(prev, k-1))
			pk := k - 1
			if down {
				pk = k + 1
			}
			px = prev[pk]
			py = px - pk
		}
		for x > px && y > py {
			ops = append(ops, Op{Equal, a[x-1]})
			x--
			y--
		}
		if d > 0 {
			if down {
				ops = append(ops, Op{Delete, a[x-1]})
				x--
			} else {
				ops = append(ops, Op{Insert, b[y-1]})
				y--
			}
		}
	}
	reverse(ops)
	return ops
}

func get(m map[int]int, k int) int {
	if x, ok := m[k]; ok {
		return x
	}
	return 0
}

func reverse(ops []Op) {
	for i, j := 0, len(ops)-1; i < j; i, j = i+1, j-1 {
		ops[i], ops[j] = ops[j], ops[i]
	}
}
