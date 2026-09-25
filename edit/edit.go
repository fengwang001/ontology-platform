// Package edit computes a shortest edit script between two line
// sequences using Myers' O(ND) algorithm with a distance limit.
package edit

import (
	"bytes"
	"errors"
	"sync/atomic"
)

// ErrTooBig is returned when the edit distance exceeds the configured limit.
var ErrTooBig = errors.New("edit: difference too large")

// DefaultMaxDist is used when Diff is called with maxDist <= 0.
const DefaultMaxDist = 1024

// Kind is the type of one edit operation.
type Kind byte

const (
	Keep Kind = iota // line present in both sequences
	Del              // line only in the old sequence
	Ins              // line only in the new sequence
)

// Op is one step of an edit script.
type Op struct{ Kind Kind }

// steps counts diagonal advances (including per-line snake comparisons)
// of the most recent Diff call. Unexported per spec; read via Steps.
var steps atomic.Int64

// Steps returns the diagonal-step counter of the most recent Diff call.
func Steps() int64 { return steps.Load() }

// Diff returns a shortest edit script turning a into b. Ties between
// shortest scripts are resolved by preferring deletion over insertion
// (see DESIGN.md). It returns ErrTooBig if the distance exceeds maxDist.
func Diff(a, b [][]byte, maxDist int) ([]Op, error) {
	steps.Store(0)
	if maxDist <= 0 {
		maxDist = DefaultMaxDist
	}
	lo := 0
	for lo < len(a) && lo < len(b) && bytes.Equal(a[lo], b[lo]) {
		lo++
		steps.Add(1)
	}
	ha, hb := len(a), len(b)
	for ha > lo && hb > lo && bytes.Equal(a[ha-1], b[hb-1]) {
		ha--
		hb--
		steps.Add(1)
	}
	mid, err := myers(a[lo:ha], b[lo:hb], maxDist)
	if err != nil {
		return nil, err
	}
	ops := make([]Op, 0, len(mid)+lo+len(a)-ha)
	for i := 0; i < lo; i++ {
		ops = append(ops, Op{Keep})
	}
	ops = append(ops, mid...)
	for i := ha; i < len(a); i++ {
		ops = append(ops, Op{Keep})
	}
	return ops, nil
}

// myers runs the greedy O(ND) search keeping a trace for backtracking.
func myers(a, b [][]byte, maxDist int) ([]Op, error) {
	n, m := len(a), len(b)
	var trace [][]int
	var prev []int
	end := -1
	for d := 0; ; d++ {
		if d > maxDist {
			return nil, ErrTooBig
		}
		cur := make([]int, 2*d+1)
		for k := -d; k <= d; k += 2 {
			steps.Add(1)
			var x int
			switch {
			case d == 0:
			case k == -d:
				x = prev[k+d]
			case k == d:
				x = prev[k+d-2] + 1
			default:
				if prev[k+d-2] < prev[k+d] {
					x = prev[k+d]
				} else {
					x = prev[k+d-2] + 1
				}
			}
			y := x - k
			for x < n && y < m && bytes.Equal(a[x], b[y]) {
				x++
				y++
				steps.Add(1)
			}
			cur[k+d] = x
			if x >= n && y >= m {
				end = d
				break
			}
		}
		trace = append(trace, cur)
		if end >= 0 {
			break
		}
		prev = cur
	}
	return backtrack(trace, end, n, m), nil
}

// backtrack replays the trace from the end, emitting ops in reverse.
// Tie-breaking mirrors the forward pass: deletion is preferred.
func backtrack(trace [][]int, dist, n, m int) []Op {
	var rev []Op
	x, y := n, m
	for d := dist; d >= 1; d-- {
		v := trace[d-1]
		k := x - y
		down := k == -d || (k != d && v[k+d-2] < v[k+d])
		var px, py int
		if down {
			px = v[k+d]
			py = px - (k + 1)
		} else {
			px = v[k+d-2]
			py = px - (k - 1)
		}
		for x > px && y > py {
			rev = append(rev, Op{Keep})
			x--
			y--
		}
		if down {
			rev = append(rev, Op{Ins})
			y--
		} else {
			rev = append(rev, Op{Del})
			x--
		}
	}
	for x > 0 && y > 0 {
		rev = append(rev, Op{Keep})
		x--
		y--
	}
	ops := make([]Op, len(rev))
	for i, op := range rev {
		ops[len(rev)-1-i] = op
	}
	return ops
}
