package edit

import (
	"bytes"
	"errors"

	"ontology/lines"
)

// Kind identifies one edit-script operation.
type Kind uint8

const (
	Equal Kind = iota
	Delete
	Insert
)

// Op describes matching, removal, or insertion of one line.
type Op struct {
	Kind     Kind
	Old      lines.Line
	New      lines.Line
	OldIndex int
	NewIndex int
}

// ErrTooDifferent means D exceeded the configured limit.
var ErrTooDifferent = errors.New("edit distance exceeds limit")

// Engine runs one diff and records equality comparisons.
type Engine struct {
	MaxD int

	comparisons uint64
}

// Comparisons reports equality probes made by the latest Diff call.
func (e *Engine) Comparisons() uint64 { return e.comparisons }

// Diff returns a shortest edit script.
func (e *Engine) Diff(a, b []lines.Line) ([]Op, error) {
	e.comparisons = 0
	n, m := len(a), len(b)
	if e.MaxD < 0 || e.MaxD > n+m {
		e.MaxD = n + m
	}
	v := map[int]int{0: 0}
	trace := make([]map[int]int, 0, e.MaxD+1)
	found := false
	for d := 0; d <= e.MaxD; d++ {
		snapshot := make(map[int]int, len(v))
		for k, x := range v {
			snapshot[k] = x
		}
		trace = append(trace, snapshot)
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[k-1] < v[k+1]) {
				x = v[k+1]
			} else {
				x = v[k-1] + 1
			}
			y := x - k
			for x < n && y < m && e.equal(a[x], b[y]) {
				x, y = x+1, y+1
			}
			v[k] = x
			if x >= n && y >= m {
				found = true
				break
			}
		}
		if found {
			return e.rebuild(a, b, trace, len(trace)-1)
		}
	}
	return nil, ErrTooDifferent
}

func (e *Engine) equal(a, b lines.Line) bool {
	e.comparisons++
	return bytes.Equal(a.Content, b.Content) && bytes.Equal(a.Newline, b.Newline)
}

func (e *Engine) rebuild(a, b []lines.Line, trace []map[int]int, d int) ([]Op, error) {
	x, y := len(a), len(b)
	var ops []Op
	for ; d > 0; d-- {
		prev := trace[d-1]
		k := x - y
		var px, py, pk int
		if k == -d || (k != d && prev[k-1] < prev[k+1]) {
			pk = k + 1
			px, py = prev[pk], prev[pk]-pk
			ops = append(ops, Op{Kind: Insert, New: b[py], OldIndex: px, NewIndex: py})
		} else {
			pk = k - 1
			px, py = prev[pk], prev[pk]-pk
			ops = append(ops, Op{Kind: Delete, Old: a[px], OldIndex: px, NewIndex: py})
		}
		x, y = px, py
		for x > 0 && y > 0 && e.equal(a[x-1], b[y-1]) {
			x, y = x-1, y-1
			ops = append(ops, Op{Kind: Equal, Old: a[x], New: b[y], OldIndex: x, NewIndex: y})
		}
	}
	for x > 0 && y > 0 && e.equal(a[x-1], b[y-1]) {
		x, y = x-1, y-1
		ops = append(ops, Op{Kind: Equal, Old: a[x], New: b[y], OldIndex: x, NewIndex: y})
	}
	if x != 0 || y != 0 {
		return nil, ErrTooDifferent
	}
	reverse(ops)
	return ops, nil
}

func reverse(ops []Op) {
	for i, j := 0, len(ops)-1; i < j; i, j = i+1, j-1 {
		ops[i], ops[j] = ops[j], ops[i]
	}
}
