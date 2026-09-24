// Package edit computes a shortest edit script between two line sequences
// with the Myers O(ND) greedy algorithm. Ties are broken in favor of
// deletions (see DESIGN.md section 3).
package edit

import (
	"bytes"
	"errors"
	"sync/atomic"

	"ontology/lines"
)

// Op is one script operation: Kind is ' ' (equal), '-' (delete from A)
// or '+' (insert from B). A/B are 0-based line indexes, -1 when unused.
type Op struct {
	Kind byte
	A, B int
}

// Script is the result of Diff: the split inputs plus the shortest script.
type Script struct {
	A, B [][]byte
	Ops  []Op
}

// ErrTooLarge is returned when the edit distance exceeds the configured limit.
var ErrTooLarge = errors.New("edit: edit distance exceeds limit")

// steps counts diagonal (snake) steps of the most recent Diff, including each
// pairwise line comparison made while following a snake.
var steps atomic.Int64

// LastSteps returns the snake-step counter of the most recent Diff.
func LastSteps() int64 { return steps.Load() }

// Diff computes the shortest edit script between a and b. limit caps the edit
// distance (deletions + insertions); a negative limit means unlimited.
func Diff(a, b []byte, limit int) (Script, error) {
	la, lb := lines.Split(a), lines.Split(b)
	ops, n, m := []Op(nil), len(la), len(lb)
	maxD := n + m
	if limit >= 0 && limit < maxD {
		maxD = limit
	}
	var cnt int64
	v := make([]int, 2*maxD+3)
	off := maxD + 1
	var trace [][]int
	found := false
	d := 0
	for ; d <= maxD; d++ {
		for k := -d; k <= d; k += 2 {
			x := v[k+1+off]
			if k != -d && (k == d || v[k-1+off] >= v[k+1+off]) {
				x = v[k-1+off] + 1 // prefer deletion on ties
			}
			y := x - k
			for x < n && y < m {
				cnt++
				if !bytes.Equal(la[x], lb[y]) {
					break
				}
				x, y = x+1, y+1
			}
			v[k+off] = x
			if x >= n && y >= m {
				found = true
				break
			}
		}
		snap := make([]int, 2*d+1)
		copy(snap, v[off-d:off+d+1])
		trace = append(trace, snap)
		if found {
			break
		}
	}
	steps.Store(cnt)
	if !found {
		return Script{A: la, B: lb}, ErrTooLarge
	}
	x, y := n, m
	for ; d > 0; d-- {
		prev := trace[d-1]
		poff := d - 1
		k := x - y
		down := k == -d || (k != d && prev[k-1+poff] < prev[k+1+poff])
		pk := k - 1
		if down {
			pk = k + 1
		}
		px, py := prev[pk+poff], prev[pk+poff]-pk
		for x > px && y > py {
			ops = append(ops, Op{Kind: ' ', A: x - 1, B: y - 1})
			x, y = x-1, y-1
		}
		if down {
			ops = append(ops, Op{Kind: '+', A: -1, B: y - 1})
			y--
		} else {
			ops = append(ops, Op{Kind: '-', A: x - 1, B: -1})
			x--
		}
	}
	for x > 0 && y > 0 {
		ops = append(ops, Op{Kind: ' ', A: x - 1, B: y - 1})
		x, y = x-1, y-1
	}
	reverseOps(ops)
	return Script{A: la, B: lb, Ops: ops}, nil
}

func reverseOps(o []Op) {
	for i, j := 0, len(o)-1; i < j; i, j = i+1, j-1 {
		o[i], o[j] = o[j], o[i]
	}
}
