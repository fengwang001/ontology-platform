// Package edit computes shortest edit scripts between line sequences using
// the Myers O(ND) greedy algorithm. Ties are broken in favor of deletions,
// matching GNU diff's ordering ('-' lines before '+' lines).
package edit

import (
	"bytes"
	"errors"
	"fmt"
)

// ErrTooLarge is returned when the edit distance exceeds the requested limit.
var ErrTooLarge = errors.New("edit: edit distance exceeds limit")

// DefaultMaxDist is used when a negative limit is passed to Diff.
const DefaultMaxDist = 1024

// Op is one script step. Kind is ' ' (keep), '-' (delete) or '+' (insert).
// Old and New are 0-based line indices; keep uses both, delete uses Old,
// insert uses New; the other field carries the count already consumed on
// that side (the insertion/removal point), needed for zero-count headers.
type Op struct {
	Kind     byte
	Old, New int
}

type Script []Op

// Distance is the number of deleted plus inserted ops.
func (s Script) Distance() int {
	d := 0
	for _, op := range s {
		if op.Kind != ' ' {
			d++
		}
	}
	return d
}

var lastSteps int64

// Steps returns the number of diagonal moves counted by the most recent Diff,
// including every line comparison performed inside snakes.
func Steps() int64 { return lastSteps }

// Diff computes a shortest edit script from a to b. maxDist caps D; exceeding
// it returns ErrTooLarge and still leaves Steps() bounded.
func Diff(a, b [][]byte, maxDist int) (Script, error) {
	if maxDist < 0 {
		maxDist = DefaultMaxDist
	}
	n, m := len(a), len(b)
	if maxDist > n+m {
		maxDist = n + m
	}
	off := maxDist + 1
	v := make([]int, 2*off+1)
	trace := make([][]int, 0, maxDist+1)
	steps := int64(0)
	for d := 0; d <= maxDist; d++ {
		trace = append(trace, append([]int(nil), v[off-d:off+d+1]...))
		for k := -d; k <= d; k += 2 {
			steps++
			var x int
			if k == -d || (k != d && v[k-1+off] < v[k+1+off]) {
				x = v[k+1+off] // down: insertion
			} else {
				x = v[k-1+off] + 1 // right: deletion wins ties
			}
			y := x - k
			for x < n && y < m && bytes.Equal(a[x], b[y]) {
				steps++
				x++
				y++
			}
			v[k+off] = x
			if x >= n && y >= m {
				script := backtrace(trace, d, n, m)
				lastSteps = steps
				return script, nil
			}
		}
	}
	lastSteps = steps
	return nil, fmt.Errorf("%w: max=%d", ErrTooLarge, maxDist)
}

// backtrace reconstructs the script. trace[d] is the V snapshot on
// diagonals [-d, d] before iteration d.
func backtrace(trace [][]int, D, n, m int) Script {
	var rev Script
	x, y := n, m
	for d := D; d > 0; d-- {
		row := trace[d]
		get := func(k int) int { return row[k+d] }
		k := x - y
		var prevK int
		if k == -d || (k != d && get(k-1) < get(k+1)) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevX, prevY := get(prevK), get(prevK)-prevK
		moveX, moveY := prevX, prevY
		if prevK == k-1 {
			moveX++
		} else {
			moveY++
		}
		for x > moveX && y > moveY {
			rev = append(rev, Op{Kind: ' ', Old: x - 1, New: y - 1})
			x--
			y--
		}
		if prevK == k-1 {
			rev = append(rev, Op{Kind: '-', Old: prevX, New: prevY})
			x, y = prevX, prevY
		} else {
			rev = append(rev, Op{Kind: '+', Old: prevX, New: prevY})
			x, y = prevX, prevY
		}
	}
	for x > 0 && y > 0 {
		rev = append(rev, Op{Kind: ' ', Old: x - 1, New: y - 1})
		x--
		y--
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}
