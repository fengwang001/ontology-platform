// Package edit computes shortest edit scripts between two line sequences
// using Myers' O(ND) algorithm with an optional distance limit.
package edit

import (
	"errors"
	"sync/atomic"
)

// ErrTooBig is returned when the edit distance exceeds the configured limit.
var ErrTooBig = errors.New("edit: distance exceeds limit")

// Op is one step of an edit script: ' ' keep, '-' delete, '+' insert.
type Op struct {
	Kind byte
	Line string
}

var steps atomic.Int64

// Steps returns the number of diagonal moves (including per-line snake
// comparisons) performed by the most recent Diff call.
func Steps() int64 { return steps.Load() }

// Diff returns a shortest edit script turning a into b. Ties between
// equally short scripts are resolved by preferring deletion over
// insertion, so within a hunk '-' lines precede '+' lines.
// If limit >= 0 and the distance exceeds limit, ErrTooBig is returned.
func Diff(a, b []string, limit int) ([]Op, error) {
	n, m := len(a), len(b)
	max := n + m
	if limit >= 0 && limit < max {
		max = limit
	}
	steps.Store(0)
	if n == 0 && m == 0 {
		return nil, nil
	}
	off := max
	v := make([]int, 2*max+1)
	var trace [][]int
	d, found := 0, false
	for ; d <= max; d++ {
		for k := -d; k <= d; k += 2 {
			steps.Add(1)
			var x int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1]
			} else {
				x = v[off+k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				steps.Add(1)
				x++
				y++
			}
			v[off+k] = x
			if x >= n && y >= m {
				found = true
				break
			}
		}
		vc := make([]int, len(v))
		copy(vc, v)
		trace = append(trace, vc)
		if found {
			break
		}
	}
	if !found {
		return nil, ErrTooBig
	}
	return backtrack(a, b, trace, d), nil
}

func backtrack(a, b []string, trace [][]int, d int) []Op {
	ops := make([]Op, 0, len(a)+len(b))
	off := (len(trace[0]) - 1) / 2
	x, y := len(a), len(b)
	for ; d > 0; d-- {
		vp := trace[d-1]
		k := x - y
		down := k == -d || (k != d && vp[off+k-1] < vp[off+k+1])
		pk := k - 1
		if down {
			pk = k + 1
		}
		px, py := vp[off+pk], vp[off+pk]-pk
		mx, my := px+1, py
		if down {
			mx, my = px, py+1
		}
		for x > mx && y > my {
			x--
			y--
			ops = append(ops, Op{' ', a[x]})
		}
		if down {
			ops = append(ops, Op{'+', b[my-1]})
		} else {
			ops = append(ops, Op{'-', a[mx-1]})
		}
		x, y = px, py
	}
	for x > 0 {
		x--
		ops = append(ops, Op{' ', a[x]})
	}
	for i, j := 0, len(ops)-1; i < j; i, j = i+1, j-1 {
		ops[i], ops[j] = ops[j], ops[i]
	}
	return ops
}
