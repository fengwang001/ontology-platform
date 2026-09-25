// Package edit computes a shortest (Myers O(ND)) edit script between two
// line sequences. It depends only on lines.
package edit

import (
	"errors"

	"ontology/lines"
)

// OpKind identifies one edit operation.
type OpKind int

const (
	Keep   OpKind = iota // line present in both
	Delete               // line removed from a
	Insert               // line added in b
)

// Op is one element of an edit script.
type Op struct {
	Kind OpKind
	Line lines.Line
}

// Options tunes Diff. MaxDistance > 0 bounds the edit distance; exceeding it
// aborts immediately with ErrTooDifferent.
type Options struct {
	MaxDistance int
}

// ErrTooDifferent is returned when the shortest distance exceeds the cap.
var ErrTooDifferent = errors.New("edit: difference exceeds max distance")

// Script is a shortest edit script plus diagnostics.
type Script struct {
	Ops      []Op
	Distance int
	steps    int64 // diagonal (snake) comparisons made by the last Diff
}

// Steps returns the total per-line diagonal comparison count.
func (s *Script) Steps() int64 { return s.steps }

type line = lines.Line

func eq(a, b line) bool { return string(a.Bytes()) == string(b.Bytes()) }

// Diff returns a deterministic shortest script: on ties a deletion edge is
// taken before an insertion edge (see DESIGN.md §3).
func Diff(a, b []line, opt Options) (*Script, error) {
	n, m := len(a), len(b)
	max := n + m
	if max == 0 {
		return &Script{}, nil
	}
	off := max + 1
	v := make([]int, 2*max+3)
	trace := make([][]int, 0)
	var steps int64
	found := false
	d := 0
	for ; d <= max; d++ {
		if opt.MaxDistance > 0 && d > opt.MaxDistance {
			return &Script{steps: steps}, ErrTooDifferent
		}
		snap := append([]int(nil), v...)
		trace = append(trace, snap)
		for k := -d; k <= d; k += 2 {
			var x int
			down := k == -d || (k != d && v[off+k-1] <= v[off+k+1])
			if down {
				x = v[off+k+1]
			} else {
				x = v[off+k-1] + 1
			}
			y := x - k
			for x < n && y < m {
				steps++
				if !eq(a[x], b[y]) {
					break
				}
				x++
				y++
			}
			v[off+k] = x
			if x >= n && y >= m {
				found = true
				break
			}
		}
		if found {
			break
		}
	}
	ops := backtrack(a, b, trace, off, d)
	dist := 0
	for _, o := range ops {
		if o.Kind != Keep {
			dist++
		}
	}
	return &Script{Ops: ops, Distance: dist, steps: steps}, nil
}

func backtrack(a, b []line, trace [][]int, off, dlast int) []Op {
	x, y := len(a), len(b)
	rev := make([]Op, 0)
	for d := dlast; d >= 0; d-- {
		v := trace[d]
		k := x - y
		down := k == -d || (k != d && v[off+k-1] <= v[off+k+1])
		pk := k - 1
		if down {
			pk = k + 1
		}
		px := v[off+pk]
		py := px - pk
		for x > px && y > py {
			rev = append(rev, Op{Keep, a[x-1]})
			x--
			y--
		}
		if d > 0 {
			if x > px {
				rev = append(rev, Op{Delete, a[x-1]})
				x--
			} else if y > py {
				rev = append(rev, Op{Insert, b[y-1]})
				y--
			}
		}
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}
