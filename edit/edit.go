// Package edit computes a shortest edit script between two line sequences
// using the Myers O(ND) algorithm.
package edit

import (
	"errors"

	"ontology/lines"
)

// ErrDistanceExceeded is returned when the edit distance exceeds the cap.
var ErrDistanceExceeded = errors.New("edit: distance exceeds configured limit")

// Kind identifies an edit operation.
type Kind uint8

// Operation kinds.
const (
	Equal Kind = iota
	Delete
	Insert
)

// Op is one run of the edit script; line carries its original terminator.
type Op struct {
	Kind Kind
	Line lines.Line
}

// Script is a shortest sequence of operations.
type Script struct {
	Ops []Op
	// Distance is the number of deleted plus inserted lines.
	Distance int
	// compares counts diagonal line comparisons made by the last Diff.
	compares int64
}

// Compares returns the snake comparison counter of the script.
func (s *Script) Compares() int64 { return s.compares }

type differ struct {
	a, b    []lines.Line
	comp    int64
	history []map[int]int
}

// Diff returns a shortest edit script. maxDistance caps the search; pass a
// negative value for no cap. Ties resolve in favour of deletions.
func Diff(a, b []lines.Line, maxDistance int) (*Script, error) {
	d := &differ{a: a, b: b}
	n, m := len(a), len(b)
	v := map[int]int{1: 0}
	for dd := 0; dd <= n+m; dd++ {
		if maxDistance >= 0 && dd > maxDistance {
			return nil, ErrDistanceExceeded
		}
		snap := make(map[int]int, len(v))
		for k, x := range v {
			snap[k] = x
		}
		d.history = append(d.history, snap)
		for k := -dd; k <= dd; k += 2 {
			x, down := v[k-1], true
			if r, has := v[k+1]; has && r >= x {
				x, down = r, false
			}
			y := x - k
			if down {
				x++
			} else {
				y++
			}
			for x < n && y < m {
				d.comp++
				if !lines.Equal(d.a[x], d.b[y]) {
					break
				}
				x, y = x+1, y+1
			}
			v[k] = x
			if x >= n && y >= m {
				ops := d.backtrack(dd)
				return &Script{Ops: ops, Distance: dd, compares: d.comp}, nil
			}
		}
	}
	return nil, ErrDistanceExceeded
}

func (d *differ) backtrack(dd int) []Op {
	var ops []Op
	x, y := len(d.a), len(d.b)
	for depth := dd; depth > 0; depth-- {
		v := d.history[depth]
		k := x - y
		px, down := v[k-1], true
		if r, has := v[k+1]; has && r >= px {
			px, down = r, false
		}
		py := px - k
		for x > px && y > py {
			ops = append(ops, Op{Kind: Equal, Line: d.a[x-1]})
			x, y = x-1, y-1
		}
		if depth > 0 {
			if down {
				ops = append(ops, Op{Kind: Delete, Line: d.a[x-1]})
				x--
			} else {
				ops = append(ops, Op{Kind: Insert, Line: d.b[y-1]})
				y--
			}
		}
	}
	for x > 0 {
		ops = append(ops, Op{Kind: Equal, Line: d.a[x-1]})
		x--
	}
	for i, j := 0, len(ops)-1; i < j; i, j = i+1, j-1 {
		ops[i], ops[j] = ops[j], ops[i]
	}
	return ops
}
