// Package edit computes a shortest edit script between two line sequences.
package edit

import (
	"errors"

	"ontology/lines"
)

// ErrTooDifferent means the edit distance exceeded the configured cap.
var ErrTooDifferent = errors.New("edit: edit distance exceeds limit")

// Op kinds: ' ' equal, '-' delete (old), '+' insert (new).
type Op struct {
	Kind byte
	Line lines.Line
}

// Script is an ordered shortest edit script.
type Script []Op

// Distance returns deletions plus insertions.
func (s Script) Distance() int {
	n := 0
	for _, op := range s {
		if op.Kind != ' ' {
			n++
		}
	}
	return n
}

var steps int64

// Steps reports diagonal steps (including per-line snake comparisons) of the
// most recent Diff call.
func Steps() int64 { return steps }

// Diff returns a shortest script via Myers O(ND). Ties prefer deletion. A
// maxD > 0 caps the edit distance; maxD <= 0 means unlimited.
func Diff(a, b []lines.Line, maxD int) (Script, error) {
	steps = 0
	n, m := len(a), len(b)
	limit := n + m
	if maxD > 0 && maxD < limit {
		limit = maxD
}
	off := n + m + 1
	v := make([]int, 2*off+2)
	v[off+1] = 0
	var trace [][]int
	for d := 0; d <= limit; d++ {
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1]
			} else {
				x = v[off+k-1] + 1
			}
			y := x - k
			for x < n && y < m {
				steps++
				if !a[x].Equal(b[y]) {
					break
				}
				x++
				y++
			}
			v[off+k] = x
			if x >= n && y >= m {
				trace = append(trace, append([]int(nil), v...))
				return build(a, b, trace, off), nil
			}
		}
		trace = append(trace, append([]int(nil), v...))
	}
	return nil, ErrTooDifferent
}

func build(a, b []lines.Line, trace [][]int, off int) Script {
	x, y := len(a), len(b)
	var ops Script
	for d := len(trace) - 1; d > 0; d-- {
		prev := trace[d-1]
		k := x - y
		var pk int
		if k == -d || (k != d && prev[off+k-1] < prev[off+k+1]) {
			pk = k + 1
		} else {
			pk = k - 1
		}
		px, py := prev[off+pk], prev[off+pk]-pk
		for x > px && y > py {
			ops = append(ops, Op{' ', a[x-1]})
			x--
			y--
		}
		if x-px == 1 {
			ops = append(ops, Op{'-', a[x-1]})
			x--
		} else {
			ops = append(ops, Op{'+', b[y-1]})
			y--
		}
	}
	for x > 0 && y > 0 {
		ops = append(ops, Op{' ', a[x-1]})
		x--
		y--
	}
	for i, j := 0, len(ops)-1; i < j; i, j = i+1, j-1 {
		ops[i], ops[j] = ops[j], ops[i]
	}
	return ops
}
