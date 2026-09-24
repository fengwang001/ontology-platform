// Package edit computes shortest edit scripts between two line sequences
// using Myers' O(ND) algorithm with a configurable distance limit.
package edit

import (
	"bytes"
	"errors"

	"ontology/lines"
)

// Op is one step of an edit script: ' ' keeps a line, '-' deletes a[Old],
// '+' inserts b[New]. Unused indices are -1.
type Op struct {
	Kind     byte
	Old, New int
}

// ErrTooLarge reports an edit distance above the configured limit.
var ErrTooLarge = errors.New("edit: distance exceeds limit")

var steps int // diagonal steps (incl. snake comparisons) of the last Diff

// LastSteps returns the step counter of the most recent Diff call.
func LastSteps() int { return steps }

// Diff returns a shortest script turning a into b. Ties between shortest
// scripts are resolved by preferring deletion over insertion. A limit <= 0
// means unbounded; exceeding the limit yields ErrTooLarge.
func Diff(a, b []byte, limit int) ([]Op, error) {
	al, bl := lines.Split(a), lines.Split(b)
	steps = 0
	n, m := len(al), len(bl)
	pre := 0
	for pre < n && pre < m && bytes.Equal(al[pre], bl[pre]) {
		pre++
		steps++
	}
	suf := 0
	for suf < n-pre && suf < m-pre && bytes.Equal(al[n-1-suf], bl[m-1-suf]) {
		suf++
		steps++
	}
	mid, err := myers(al[pre:n-suf], bl[pre:m-suf], limit)
	if err != nil {
		return nil, err
	}
	ops := make([]Op, 0, pre+suf+len(mid))
	for i := 0; i < pre; i++ {
		ops = append(ops, Op{' ', i, i})
	}
	for _, op := range mid {
		if op.Old >= 0 {
			op.Old += pre
		}
		if op.New >= 0 {
			op.New += pre
		}
		ops = append(ops, op)
	}
	for i := 0; i < suf; i++ {
		ops = append(ops, Op{' ', n - suf + i, m - suf + i})
	}
	return ops, nil
}

func myers(a, b [][]byte, limit int) ([]Op, error) {
	n, m := len(a), len(b)
	max := n + m
	if limit > 0 && limit < max {
		max = limit
	}
	off := max + 1
	v := make([]int, 2*max+3)
	var trace [][]int
	found := -1
	for d := 0; d <= max; d++ {
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1] // down: insertion
			} else {
				x = v[off+k-1] + 1 // right: deletion wins ties
			}
			y := x - k
			steps++
			for x < n && y < m && bytes.Equal(a[x], b[y]) {
				x++
				y++
				steps++
			}
			v[off+k] = x
			if x >= n && y >= m {
				found = d
				break
			}
		}
		w := make([]int, 2*d+1)
		copy(w, v[off-d:off+d+1])
		trace = append(trace, w)
		if found >= 0 {
			break
		}
	}
	if found < 0 {
		return nil, ErrTooLarge
	}
	var rev []Op
	x, y := n, m
	for d := found; d >= 1; d-- {
		prev := trace[d-1]
		k := x - y
		get := func(kk int) int { return prev[kk+d-1] }
		pk := k - 1
		if k == -d || (k != d && get(k-1) < get(k+1)) {
			pk = k + 1
		}
		px, py := get(pk), get(pk)-pk
		for x > px && y > py {
			x--
			y--
			rev = append(rev, Op{' ', x, y})
		}
		if pk == k-1 {
			x--
			rev = append(rev, Op{'-', x, -1})
		} else {
			y--
			rev = append(rev, Op{'+', -1, y})
		}
	}
	for x > 0 && y > 0 {
		x--
		y--
		rev = append(rev, Op{' ', x, y})
	}
	ops := make([]Op, len(rev))
	for i, op := range rev {
		ops[len(rev)-1-i] = op
	}
	return ops, nil
}
