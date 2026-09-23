// Package edit computes a shortest edit script between two line sequences
// with the linear-space (divide-and-conquer) Myers algorithm.
package edit

import (
	"errors"

	"ontology/lines"
)

// Kind selects an edit operation.
type Kind uint8

const (
	Equal Kind = iota
	Delete
	Insert
)

// Op is one edit operation. For Equal/Delete L is the old line; for Insert L
// is the new line.
type Op struct {
	Kind Kind
	L    lines.Line
}

// ErrTooDifferent reports that the edit distance exceeds the configured limit.
var ErrTooDifferent = errors.New("edit: distance exceeds limit")

// Result holds a shortest script and instrumentation counters.
type Result struct {
	Ops      []Op
	Distance int
	// Steps counts every per-line diagonal comparison made by the last run,
	// including comparisons inside snakes.
	Steps int
}

type differ struct {
	a, b  []lines.Line
	limit int
	steps int
}

// Diff returns a shortest edit script. limit (>=0) bounds the distance; when
// the true distance is greater, ErrTooDifferent is returned.
func Diff(a, b []lines.Line, limit int) (Result, error) {
	d := &differ{a: a, b: b, limit: limit}
	ops, err := d.ses(0, len(a), 0, len(b))
	if err != nil {
		return Result{Steps: d.steps}, err
	}
	res := Result{Ops: ops, Steps: d.steps}
	for _, o := range ops {
		if o.Kind != Equal {
			res.Distance++
		}
	}
	return res, nil
}

func (d *differ) ses(x0, x1, y0, y1 int) ([]Op, error) {
	var ops []Op
	for x0 < x1 && y0 < y1 && lines.Equal(d.a[x0], d.b[y0]) {
		ops = append(ops, Op{Equal, d.a[x0]})
		x0++
		y0++
	}
	tail := 0
	for x0 < x1-tail && y0 < y1-tail && tail < x1-x0 && tail < y1-y0 &&
		lines.Equal(d.a[x1-1-tail], d.b[y1-1-tail]) {
		tail++
	}
	if tail > 0 {
		x1 -= tail
		y1 -= tail
	}
	if x0 == x1 {
		for ; y0 < y1; y0++ {
			ops = append(ops, Op{Insert, d.b[y0]})
		}
	} else if y0 == y1 {
		for ; x0 < x1; x0++ {
			ops = append(ops, Op{Delete, d.a[x0]})
		}
	} else {
		mx, my, ux, uy := d.middle(x0, x1, y0, y1)
		left, err := d.ses(x0, mx, y0, my)
		if err != nil {
			return nil, err
		}
		mid := make([]Op, 0, ux-mx+uy-my)
		for mx < ux {
			mid = append(mid, Op{Delete, d.a[mx]})
			mx++
		}
		for my < uy {
			mid = append(mid, Op{Insert, d.b[my]})
			my++
		}
		right, err := d.ses(ux, x1, uy, y1)
		if err != nil {
			return nil, err
		}
		ops = append(ops, left...)
		ops = append(ops, mid...)
		ops = append(ops, right...)
	}
	for i := 0; i < tail; i++ {
		ops = append(ops, Op{Equal, d.a[x1+i]})
	}
	if d.count(ops) > d.limit {
		return nil, ErrTooDifferent
	}
	return ops, nil
}

func (d *differ) count(ops []Op) int {
	n := 0
	for _, o := range ops {
		if o.Kind != Equal {
			n++
		}
	}
	return n
}

func (d *differ) middle(x0, x1, y0, y1 int) (int, int, int, int) {
	n, m := x1-x0, y1-y0
	delta := n - m
	odd := delta&1 == 1
	maxD := (n + m + 1) / 2
	vf := make([]int, 2*maxD+3)
	vr := make([]int, 2*maxD+3)
	set := func(v []int, k, x int) { v[k+maxD+1] = x }
	get := func(v []int, k int) int { return v[k+maxD+1] }
	for dd := 0; dd <= maxD; dd++ {
		for k := -dd; k <= dd; k += 2 {
			var x int
			if k == -dd || (k != dd && get(vf, k-1)+1 < get(vf, k+1)) {
				x = get(vf, k+1) // right edge: insert
			} else {
				x = get(vf, k-1) + 1 // down edge: delete (preferred on tie)
			}
			y := x - k
			for x < n && y < m {
				d.steps++
				if !lines.Equal(d.a[x0+x], d.b[y0+y]) {
					break
				}
				x++
				y++
			}
			set(vf, k, x)
			if odd {
				kr := k - delta
				if kr >= -(dd - 1) && kr <= dd-1 {
					rx := get(vr, kr)
					if x >= rx {
						return x0 + x, y0 + y, x0 + x, y0 + y
					}
				}
			}
		}
		for k := -dd; k <= dd; k += 2 {
			var x int
			if k == -dd || (k != dd && get(vr, k-1) < get(vr, k+1)-1) {
				x = get(vr, k-1) // up edge
			} else {
				x = get(vr, k+1) - 1 // left edge
			}
			y := n - (x + k)
			for x > 0 && y > 0 {
				d.steps++
				if !lines.Equal(d.a[x0+x-1], d.b[y0+y-1]) {
					break
				}
				x--
				y--
			}
			set(vr, k, x)
			if !odd {
				kf := k + delta
				if kf >= -dd && kf <= dd {
					fx := get(vf, kf)
					if fx >= x {
						return x0 + x, y0 + y, x0 + x, y0 + y
					}
				}
			}
		}
	}
	panic("edit: middle snake not found")
}
