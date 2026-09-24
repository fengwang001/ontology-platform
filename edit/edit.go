// Package edit computes a shortest edit script between two line sequences
// using Myers' O(ND) algorithm. It depends only on package lines.
package edit

import (
	"bytes"
	"errors"

	"ontology/lines"
)

// Kind identifies one edit operation.
type Kind int

const (
	// Equal means the line is present in both files (context).
	Equal Kind = iota
	// Delete means the line exists only in the old file.
	Delete
	// Insert means the line exists only in the new file.
	Insert
)

// Op is one element of an edit script.
type Op struct {
	Kind    Kind
	OldLine lines.Line // valid for Equal and Delete
	NewLine lines.Line // valid for Equal and Insert
}

// ErrTooDifferent is returned when the edit distance exceeds the configured cap.
var ErrTooDifferent = errors.New("edit: diff exceeds maximum edit distance")

// Counter counts line comparisons performed during snakes of the latest Diff.
type Counter struct{ steps int64 }

// Steps returns the total number of single-line comparisons counted.
func (c *Counter) Steps() int64 { return c.steps }

type snap struct {
	down bool
}

type layer struct {
	v    []int
	down []bool
	kmin int
}

// Diff runs Myers with an edit-distance cap maxD (<0 means unlimited). Among
// shortest scripts it prefers Delete on ties (DESIGN.md §3). ctr, when non-nil,
// counts every single-line comparison attempted while advancing a snake.
func Diff(a, b []lines.Line, maxD int, ctr *Counter) ([]Op, error) {
	n, m := len(a), len(b)
	if maxD < 0 {
		maxD = n + m
	}
	layers := []layer{{v: []int{0}, down: []bool{false}, kmin: 0}}
	for d := 0; d <= maxD; d++ {
		prev := layers[d]
		kmin, kmax := -d, d
		size := kmax - kmin + 1
		cur := layer{v: make([]int, size), down: make([]bool, size), kmin: kmin}
		for k := kmin; k <= kmax; k++ {
			idx := k - kmin
			down := k == -d || (k != d && prev.v[k-1-prev.kmin] < prev.v[k+1-prev.kmin])
			var x int
			if down {
				x = prev.v[k+1-prev.kmin] // move down: insert
			} else {
				x = prev.v[k-1-prev.kmin] + 1 // move right: delete (tie winner)
			}
			y := x - k
			for x < n && y < m {
				if ctr != nil {
					ctr.steps++
				}
				if !bytes.Equal(a[x].Data, b[y].Data) {
					break
				}
				x++
				y++
			}
			cur.v[idx] = x
			cur.down[idx] = down
		}
		layers = append(layers, cur)
		if cur.v[m-cur.kmin] == n {
			return backtrack(layers, a, b), nil
		}
	}
	return nil, ErrTooDifferent
}

func backtrack(layers []layer, a, b []lines.Line) []Op {
	var ops []Op
	k, x, y := len(b), len(a), len(b)
	for d := len(layers) - 1; d > 0; d-- {
		down := layers[d].down[k-layers[d].kmin]
		pk := k - 1
		if down {
			pk = k + 1
		}
		p := layers[d-1]
		px := p.v[pk-p.kmin]
		py := px - pk
		for x > px && y > py && bytes.Equal(a[x-1].Data, b[y-1].Data) {
			ops = append(ops, Op{Kind: Equal, OldLine: a[x-1], NewLine: b[y-1]})
			x--
			y--
		}
		if down {
			ops = append(ops, Op{Kind: Insert, NewLine: b[y-1]})
			y--
		} else {
			ops = append(ops, Op{Kind: Delete, OldLine: a[x-1]})
			x--
		}
		k = pk
	}
	for x > 0 {
		ops = append(ops, Op{Kind: Equal, OldLine: a[x-1], NewLine: b[y-1]})
		x--
		y--
	}
	for i, j := 0, len(ops)-1; i < j; i, j = i+1, j-1 {
		ops[i], ops[j] = ops[j], ops[i]
	}
	return ops
}
