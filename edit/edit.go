// Package edit computes a shortest edit script between two line
// sequences with the Myers O(ND) algorithm.
package edit

import (
	"bytes"
	"errors"

	"ontology/lines"
)

// Kind selects what an edit operation does.
type Kind uint8

const (
	Equal Kind = iota
	Delete
	Insert
)

// Op is one edit operation. For Equal/Delete it references a line of a;
// for Insert it references a line of b.
type Op struct {
	Kind Kind
	A    lines.Line
	B    lines.Line
}

// Stats reports the last Diff: Distance is deletes+inserts, Steps is
// the total diagonal advance work including every snake comparison.
type Stats struct {
	Distance int
	Steps    int64
}

// ErrTooLarge means the edit distance exceeded the configured limit.
var ErrTooLarge = errors.New("edit: edit distance exceeds configured limit")

// Diff returns a shortest edit script transforming a into b. On ties
// it deletes before inserting (see DESIGN.md). If maxDist >= 0 and the
// distance would exceed it, Diff returns ErrTooLarge.
func Diff(a, b []lines.Line, maxDist int) ([]Op, Stats, error) {
	n, m := len(a), len(b)
	var steps int64
	// trace[d] holds V[k] for k in [-d,d] as 2d+1 entries (index k+d).
	trace := make([][]int, 0, n+m+1)
	v := make([]int, 1)
	found := false
	dmin := -1
	for d := 0; d <= n+m; d++ {
		if maxDist >= 0 && d > maxDist {
			return nil, Stats{Steps: steps}, ErrTooLarge
		}
		nv := make([]int, 2*d+1)
		get := func(k int) int {
			if k < -(d-1) || k > d-1 {
				return -1
			}
			return v[k+(d-1)]
		}
		for k := -d; k <= d; k += 2 {
			var x int
			if d == 0 {
				x = 0 // classic Myers seed V[1] = 0
			} else if k == -d || (k != d && get(k-1) < get(k+1)) {
				// Ties go down (delete first); see DESIGN.md.
				x = get(k + 1)
			} else {
				x = get(k-1) + 1
			}
			y := x - k
			for x < n && y < m {
				steps++ // one diagonal comparison, failing one included
				if !bytes.Equal(a[x].Raw, b[y].Raw) {
					break
				}
				x, y = x+1, y+1
			}
			nv[k+d] = x
			if x >= n && y >= m {
				found, dmin = true, d
			}
		}
		trace = append(trace, nv)
		v = nv
		if found {
			break
		}
	}
	var rev []Op
	x, y := n, m
	for d := dmin; d > 0; d-- {
		pv := trace[d-1]
		k := x - y
		get := func(kk int) int {
			if kk < -(d-1) || kk > d-1 {
				return -1
			}
			return pv[kk+(d-1)]
		}
		var pk int
		if k == -d || (k != d && get(k-1) < get(k+1)) {
			pk = k + 1
		} else {
			pk = k - 1
		}
		px, py := get(pk), get(pk)-pk
		for x > px && y > py {
			rev = append(rev, Op{Kind: Equal, A: a[x-1], B: b[y-1]})
			x, y = x-1, y-1
		}
		if x == px {
			rev = append(rev, Op{Kind: Insert, B: b[py]})
		} else {
			rev = append(rev, Op{Kind: Delete, A: a[px]})
		}
		x, y = px, py
	}
	for x > 0 && y > 0 {
		rev = append(rev, Op{Kind: Equal, A: a[x-1], B: b[y-1]})
		x, y = x-1, y-1
	}
	ops := make([]Op, len(rev))
	dist := 0
	for i := range rev {
		op := rev[len(rev)-1-i]
		if op.Kind != Equal {
			dist++
		}
		ops[i] = op
	}
	return ops, Stats{Distance: dist, Steps: steps}, nil
}
