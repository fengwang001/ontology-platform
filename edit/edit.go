// Package edit computes a shortest edit script between two line slices (Myers).
package edit

import "ontology/lines"

// Op is one grouped edit operation (a delete/insert run at one anchor).
type Op struct {
	Del []lines.Line // old-side lines (possibly empty)
	Ins []lines.Line // new-side lines (possibly empty)
}

// Script is a shortest edit script plus diagnostics.
type Script struct {
	Ops    []Op
	Dist   int // |Del| + |Ins|
	Snakes int // diagonal snake comparison steps (counter)
}

// ErrTooDifferent reports that the edit distance exceeds the configured cap.
var ErrTooDifferent = errDiff("edit: distance exceeds limit")

type errDiff string

func (e errDiff) Error() string { return string(e) }

type snap struct {
	d int
	v []int
}

func eq(x, y lines.Line) bool { return x.Text == y.Text && x.NL == y.NL }

// Diff runs Myers O(ND). maxD<=0 means use len(a)+len(b). Ties prefer delete.
func Diff(a, b []lines.Line, maxD int) (*Script, error) {
	n, m := len(a), len(b)
	if maxD <= 0 {
		maxD = n + m
	}
	off := maxD + 1
	v := make([]int, 2*maxD+3)
	snaps := make([]snap, 0, maxD+1)
	steps := 0
	for d := 0; d <= maxD; d++ {
		for k := -d; k <= d; k += 2 {
			x := next(v, off, k, d)
			y := x - k
			for x < n && y < m {
				steps++
				if !eq(a[x], b[y]) {
					break
				}
				x++
				y++
			}
			v[k+off] = x
			if x >= n && y >= m {
				snaps = append(snaps, snap{d, append([]int(nil), v...)})
				return backtrack(a, b, snaps, off, steps)
			}
		}
		snaps = append(snaps, snap{d, append([]int(nil), v...)})
	}
	return &Script{Snakes: steps}, ErrTooDifferent
}

func next(v []int, off, k, d int) int {
	switch {
	case k == -d:
		return v[k+1+off]
	case k == d:
		return v[k-1+off] + 1
	case v[k-1+off] < v[k+1+off]:
		return v[k+1+off]
	default:
		return v[k-1+off] + 1
	}
}

type pt struct{ x, y int }

func backtrack(a, b []lines.Line, snaps []snap, off, steps int) (*Script, error) {
	x, y := len(a), len(b)
	var rev []unit
	for i := len(snaps) - 1; i >= 0; i-- {
		d, v := snaps[i].d, snaps[i].v
		k := x - y
		if d == 0 {
			for x > 0 && y > 0 {
				x--
				y--
				rev = append(rev, unit{'c', x})
			}
			continue
		}
		pk := k
		switch {
		case k == -d:
			pk = k + 1
		case k == d:
			pk = k - 1
		case v[k-1+off] < v[k+1+off]:
			pk = k + 1
		default:
			pk = k - 1
		}
		var px, py int
		if pk == k-1 {
			px = v[pk+off] + 1
		} else {
			px = v[pk+off]
		}
		py = px - pk
		for x > px && y > py {
			x--
			y--
			rev = append(rev, unit{'c', x})
		}
		if pk == k-1 {
			rev = append(rev, unit{'d', px})
			x, y = px, py
		} else {
			rev = append(rev, unit{'i', py})
			x, y = px, py
		}
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	ops := group(a, b, rev)
	dist := 0
	for _, o := range ops {
		dist += len(o.Del) + len(o.Ins)
	}
	return &Script{Ops: ops, Dist: dist, Snakes: steps}, nil
}

type unit struct {
	kind byte
	idx  int
}

func group(a, b []lines.Line, us []unit) []Op {
	var ops []Op
	gap := true
	for _, st := range us {
		switch st.kind {
		case 'c':
			gap = true
		case 'd':
			if len(ops) == 0 || gap {
				ops = append(ops, Op{})
			}
			ops[len(ops)-1].Del = append(ops[len(ops)-1].Del, a[st.idx])
			gap = false
		case 'i':
			if len(ops) == 0 || gap {
				ops = append(ops, Op{})
			}
			ops[len(ops)-1].Ins = append(ops[len(ops)-1].Ins, b[st.idx])
			gap = false
		}
	}
	return ops
}
