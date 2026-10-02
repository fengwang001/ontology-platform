// Package edit computes a shortest edit script between two line sequences
// using Myers' O(ND) algorithm with a configurable distance limit.
package edit

import "errors"

// ErrTooDifferent is returned when the edit distance exceeds the limit.
var ErrTooDifferent = errors.New("edit: difference too large")

// Op is one step of an edit script. Kind is ' ' (keep), '-' (delete a[A]),
// or '+' (insert b[B]). A and B are the 0-based positions in the two
// sequences at which the op occurs.
type Op struct {
	Kind byte
	A, B int
}

// steps counts diagonal advances (including per-line snake comparisons)
// of the most recent Diff call. Unexported per spec; read via Steps.
var steps int

// Steps returns the step counter of the most recent Diff call.
func Steps() int { return steps }

// Diff returns a shortest edit script turning a into b. Ties between
// shortest scripts are broken by preferring deletions over insertions.
// maxDist limits the edit distance (<=0 means no limit); if exceeded,
// ErrTooDifferent is returned.
func Diff(a, b []string, maxDist int) ([]Op, error) {
	n, m := len(a), len(b)
	max := n + m
	if maxDist > 0 && maxDist < max {
		max = maxDist
	}
	steps = 0
	if n == 0 && m == 0 {
		return nil, nil
	}
	// v[k+offset] = furthest x reachable on diagonal k.
	v := make([]int, 2*max+3)
	off := max + 1
	var trace [][]int
	found := -1
	for d := 0; d <= max; d++ {
		snap := append([]int(nil), v...)
		trace = append(trace, snap)
		for k := -d; k <= d; k += 2 {
			steps++
			var x int
			if k == -d || (k != d && v[k-1+off] < v[k+1+off]) {
				x = v[k+1+off] // down: insertion
			} else {
				x = v[k-1+off] + 1 // right: deletion (wins ties)
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				steps++
				x++
				y++
			}
			v[k+off] = x
			if x >= n && y >= m {
				found = d
				break
			}
		}
		if found >= 0 {
			break
		}
	}
	if found < 0 {
		return nil, ErrTooDifferent
	}
	return backtrack(a, b, trace, off, found), nil
}

func backtrack(a, b []string, trace [][]int, off, d int) []Op {
	var rev []Op
	x, y := len(a), len(b)
	for ; d > 0; d-- {
		v := trace[d]
		k := x - y
		var prevK int
		if k == -d || (k != d && v[k-1+off] < v[k+1+off]) {
			prevK = k + 1 // came from an insertion
		} else {
			prevK = k - 1 // came from a deletion (wins ties)
		}
		prevX, prevY := v[prevK+off], v[prevK+off]-prevK
		for x > prevX && y > prevY {
			rev = append(rev, Op{' ', x - 1, y - 1})
			x--
			y--
		}
		if prevY == y {
			rev = append(rev, Op{'-', prevX, prevY})
		} else {
			rev = append(rev, Op{'+', prevX, prevY})
		}
		x, y = prevX, prevY
	}
	for x > 0 && y > 0 {
		rev = append(rev, Op{' ', x - 1, y - 1})
		x--
		y--
	}
	out := make([]Op, len(rev))
	for i, op := range rev {
		out[len(rev)-1-i] = op
	}
	return out
}
