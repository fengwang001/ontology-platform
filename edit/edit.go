// Package edit computes a shortest edit script between two line sequences
// using the Myers O(ND) algorithm. Ties are resolved in favour of deletions.
package edit

import (
	"errors"

	"ontology/lines"
)

// Kind is one edit operation kind.
type Kind uint8

const (
	Equal Kind = iota
	Delete
	Insert
)

// Op is one step of an edit script: Equal consumes one line from each side,
// Delete consumes one old line, Insert consumes one new line.
type Op struct {
	Kind Kind
	Old   lines.Line
	New   lines.Line
}

// ErrTooDifferent reports that the edit distance exceeded MaxDistance.
var ErrTooDifferent = errors.New("edit: distance exceeds limit")

// Options configures Diff.
type Options struct {
	MaxDistance int // <= 0 means unlimited
}

// Editor keeps counters from the most recent Diff invocation.
type Editor struct {
	// Steps counts diagonal advance steps of the latest diff, including every
	// per-line comparison attempted while extending snakes.
	Steps int64
}

// Diff returns a shortest edit script from a to b.
func (e *Editor) Diff(a, b []lines.Line, opt Options) ([]Op, error) {
	e.Steps = 0
	n, m := len(a), len(b)
	maxd := n + m
	if opt.MaxDistance > 0 && opt.MaxDistance < maxd {
		maxd = opt.MaxDistance
	}
	v := make([]int, 2*(n+m)+3)
	off := n + m + 1
	v[off+1] = 0
	layers := make([][]int, 0, 8)
	found := false
	for d := 0; d <= maxd; d++ {
		layers = append(layers, append([]int(nil), v...))
		for k := -d; k <= d; k += 2 {
			idx := off + k
			var x int
			if k == -d || (k != d && v[idx-1] < v[idx+1]) {
				x = v[idx+1] // right: insert
			} else {
				x = v[idx-1] + 1 // down: delete (preferred on ties)
			}
			y := x - k
			for x < n && y < m {
				e.Steps++
				if !lines.Equal(a[x], b[y]) {
					break
				}
				x++
				y++
			}
			v[idx] = x
			if x == n && y == m {
				found = true
				break
			}
		}
		if found {
			return trace(a, b, layers, off, d, n, m), nil
		}
	}
	return nil, ErrTooDifferent
}

func trace(a, b []lines.Line, layers [][]int, off, dEnd, n, m int) []Op {
	x, y := n, m
	ops := make([]Op, 0, n+m)
	for d := dEnd; d > 0; d-- {
		vp := layers[d]
		k := x - y
		var pk, px int
		if k == -d || (k != d && vp[off+k-1] < vp[off+k+1]) {
			pk, px = k+1, vp[off+k+1] // right move: insert
		} else {
			pk = k - 1 // down move: delete
			px = vp[off+pk] + 1
		}
		py := px - pk
		for x > px && y > py {
			ops = append(ops, Op{Kind: Equal, Old: a[x-1], New: b[y-1]})
			x--
			y--
		}
		if pk == k-1 {
			ops = append(ops, Op{Kind: Delete, Old: a[x-1]})
			x--
		} else {
			ops = append(ops, Op{Kind: Insert, New: b[y-1]})
			y--
		}
	}
	for x > 0 && y > 0 {
		ops = append(ops, Op{Kind: Equal, Old: a[x-1], New: b[y-1]})
		x--
		y--
	}
	for i, j := 0, len(ops)-1; i < j; i, j = i+1, j-1 {
		ops[i], ops[j] = ops[j], ops[i]
	}
	return ops
}
