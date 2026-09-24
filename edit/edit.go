// Package edit computes a shortest edit script between two line
// sequences using Myers' O(ND) algorithm, with an optional distance
// limit and a per-call step counter.
package edit

import (
	"bytes"
	"errors"
)

// ErrTooBig is returned when the edit distance exceeds the configured limit.
var ErrTooBig = errors.New("edit: edit distance exceeds limit")

// Op is one edit operation: ' ' keep, '-' delete, '+' insert.
type Op struct {
	Kind byte
	Text []byte
}

// Script is the result of Diff. Ties between shortest scripts are
// resolved by preferring deletion over insertion (see DESIGN.md).
type Script struct {
	Ops   []Op
	steps int // diagonal steps of the last Diff, incl. snake comparisons
}

// Steps returns the step counter of the diff that produced s.
func (s *Script) Steps() int { return s.steps }

// Diff returns a shortest edit script turning a into b.
// maxDist > 0 caps the edit distance; exceeding it yields ErrTooBig.
// On ErrTooBig the returned Script is non-nil so Steps stays readable.
func Diff(a, b [][]byte, maxDist int) (*Script, error) {
	n, m := len(a), len(b)
	s := &Script{}
	if n == 0 && m == 0 {
		return s, nil
	}
	max := n + m
	limit := max
	if maxDist > 0 && maxDist < limit {
		limit = maxDist
	}
	v := make([]int, 2*max+1)
	var trace [][]int
	d, found := 0, -1
	for ; d <= limit; d++ {
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[k-1+max] < v[k+1+max]) {
				x = v[k+1+max] // down: insertion
			} else {
				x = v[k-1+max] + 1 // right: deletion (wins ties)
			}
			y := x - k
			s.steps++
			for x < n && y < m && bytes.Equal(a[x], b[y]) {
				x, y, s.steps = x+1, y+1, s.steps+1
			}
			v[k+max] = x
			if x >= n && y >= m {
				found = d
				break
			}
		}
		cp := make([]int, len(v))
		copy(cp, v)
		trace = append(trace, cp)
		if found >= 0 {
			break
		}
	}
	if found < 0 {
		return s, ErrTooBig
	}
	x, y := n, m
	for d = found; d > 0; d-- {
		vp, k := trace[d-1], x-y
		pk := k - 1
		if k == -d || (k != d && vp[k-1+max] < vp[k+1+max]) {
			pk = k + 1
		}
		px, py := vp[pk+max], vp[pk+max]-pk
		for x > px && y > py {
			x, y = x-1, y-1
			s.Ops = append(s.Ops, Op{' ', a[x]})
		}
		if x == px {
			y--
			s.Ops = append(s.Ops, Op{'+', b[y]})
		} else {
			x--
			s.Ops = append(s.Ops, Op{'-', a[x]})
		}
	}
	for x > 0 && y > 0 {
		x, y = x-1, y-1
		s.Ops = append(s.Ops, Op{' ', a[x]})
	}
	for i, j := 0, len(s.Ops)-1; i < j; i, j = i+1, j-1 {
		s.Ops[i], s.Ops[j] = s.Ops[j], s.Ops[i]
	}
	return s, nil
}
