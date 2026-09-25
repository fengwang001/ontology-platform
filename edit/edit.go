// Package edit computes a shortest edit script between two line sequences.
package edit

import (
	"bytes"
	"errors"

	"ontology/lines"
)

// Seq is a sequence of lines.
type Seq = []lines.Line

// Op is one edit operation: ' ' equal, '-' delete from a, '+' insert from b.
type Op struct {
	Kind byte
	A    lines.Line
	B    lines.Line
}

// ErrTooDifferent reports that the edit distance exceeded the configured cap.
var ErrTooDifferent = errors.New("edit: files differ more than the allowed maximum distance")

// steps counts snake line comparisons (including the failing one) last run.
var steps int

// Steps returns the snake-step counter of the most recent Diff.
func Steps() int { return steps }

// Diff returns a shortest edit script; maxD<=0 means no cap. Ties between a
// delete and an insert of the same x are broken in favour of delete.
func Diff(a, b Seq, maxD int) ([]Op, error) {
	steps = 0
	n, m := len(a), len(b)
	v := map[int]int{1: 0} // Myers V; absent keys read as 0 (parity-filtered)
	var trace []map[int]int
	var found int
search:
	for d := 0; d <= n+m; d++ {
		if maxD > 0 && d > maxD {
			return nil, ErrTooDifferent
		}
		snap := make(map[int]int, len(v))
		for k, x := range v {
			snap[k] = x
		}
		trace = append(trace, snap)
		for k := -d; k <= d; k += 2 {
			x := step(v, k)
			y := x - k
			if x < 0 || y < 0 || x > n || y > m {
				x = -1 // outside the edit graph: poison so it cannot extend
				v[k] = x
				continue
			}
			for x < n && y < m {
				steps++
				if !eqLine(a[x], b[y]) {
					break
				}
				x++
				y++
			}
			v[k] = x
			if x >= n && y >= m {
				found = d
				break search
			}
		}
	}
	return backtrack(a, b, trace, found), nil
}

func step(v map[int]int, k int) int {
	// insert edge when only right exists, or right reaches strictly farther;
	// equal x is a tie -> take the delete edge (k-1), delete-first policy.
	if get(v, k-1) < get(v, k+1) {
		return v[k+1]
	}
	return v[k-1] + 1
}

func get(v map[int]int, k int) int {
	if x, ok := v[k]; ok {
		return x
	}
	return 0
}

func eqLine(x, y lines.Line) bool {
	return bytes.Equal(x.Text, y.Text) && bytes.Equal(x.EOL, y.EOL)
}

func backtrack(a, b Seq, trace []map[int]int, d int) []Op {
	x, y := len(a), len(b)
	var ops []Op
	for ; d > 0; d-- {
		prev := trace[d-1]
		k := x - y
		kPrev := k - 1 // delete edge wins equal-x ties
		if get(prev, k-1) < get(prev, k+1) {
			kPrev = k + 1
		}
		println("bt d", d, "k", k, "kp", kPrev, "L", get(prev, k-1), "R", get(prev, k+1))
		xp := get(prev, kPrev)
		yp := xp - kPrev
		for x > xp && y > yp {
			ops = append(ops, Op{Kind: ' ', A: a[x-1], B: b[y-1]})
			x--
			y--
		}
		if y != yp {
			ops = append(ops, Op{Kind: '+', B: b[yp]})
		} else {
			ops = append(ops, Op{Kind: '-', A: a[xp]})
		}
		x, y = xp, yp
	}
	for x > 0 && y > 0 {
		ops = append(ops, Op{Kind: ' ', A: a[x-1], B: b[y-1]})
		x--
		y--
	}
	for i, j := 0, len(ops)-1; i < j; i, j = i+1, j-1 {
		ops[i], ops[j] = ops[j], ops[i]
	}
	return ops
}
