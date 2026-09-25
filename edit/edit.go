// Package edit computes shortest edit scripts between two line
// sequences using Myers' O(ND) algorithm, with a configurable
// edit-distance limit.
package edit

import (
	"bytes"
	"errors"
)

// Op is one step of an edit script.
type Op byte

const (
	OpKeep Op = ' ' // line present in both sequences
	OpDel  Op = '-' // line deleted from the old sequence
	OpIns  Op = '+' // line inserted from the new sequence
)

// ErrTooLarge reports an edit distance above the configured limit.
var ErrTooLarge = errors.New("edit: difference too large")

var steps int

// Steps returns the total number of diagonal advances (including
// per-line snake comparisons) performed by the most recent Diff call.
func Steps() int { return steps }

// Diff returns a shortest edit script turning a into b. Ties between
// equally short paths always resolve to deletion before insertion, so
// the result is deterministic. maxDist limits the edit distance;
// exceeding it yields ErrTooLarge. maxDist <= 0 means no limit.
func Diff(a, b [][]byte, maxDist int) ([]Op, error) {
	n, m := len(a), len(b)
	steps = 0
	if n+m == 0 {
		return nil, nil
	}
	if maxDist <= 0 || maxDist > n+m {
		maxDist = n + m
	}
	off := maxDist + 1
	v := make([]int, 2*maxDist+3)
	var trace [][]int
	found := -1
dloop:
	for d := 0; d <= maxDist; d++ {
		for k := -d; k <= d; k += 2 {
			steps++
			var x int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1] // move down: insertion
			} else {
				x = v[off+k-1] + 1 // move right: deletion (wins ties)
			}
			y := x - k
			for x < n && y < m && bytes.Equal(a[x], b[y]) {
				steps++
				x++
				y++
			}
			v[off+k] = x
			if x >= n && y >= m {
				found = d
				break dloop
			}
		}
		cp := make([]int, len(v))
		copy(cp, v)
		trace = append(trace, cp)
	}
	if found < 0 {
		return nil, ErrTooLarge
	}
	ops := make([]Op, 0, n+m)
	x, y := n, m
	for d := found; d > 0; d-- {
		vp := trace[d-1]
		k := x - y
		pk := k - 1
		if k == -d || (k != d && vp[off+k-1] < vp[off+k+1]) {
			pk = k + 1
		}
		px, py := vp[off+pk], vp[off+pk]-pk
		for x > px && y > py {
			ops = append(ops, OpKeep)
			x--
			y--
		}
		if pk == k+1 {
			ops = append(ops, OpIns)
			y--
		} else {
			ops = append(ops, OpDel)
			x--
		}
	}
	for x > 0 && y > 0 {
		ops = append(ops, OpKeep)
		x--
		y--
	}
	for i, j := 0, len(ops)-1; i < j; i, j = i+1, j-1 {
		ops[i], ops[j] = ops[j], ops[i]
	}
	return ops, nil
}
