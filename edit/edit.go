// Package edit computes a shortest edit script between two line sequences
// using Myers' O(ND) algorithm. Ties are broken by preferring deletion
// over insertion (see DESIGN.md section 3), which makes the script
// deterministic and lists deletions before insertions within a hunk.
package edit

import (
	"bytes"
	"errors"

	"ontology/lines"
)

// ErrTooBig is returned when the edit distance exceeds the configured limit.
var ErrTooBig = errors.New("edit: difference too large")

// Op is one edit step. Kind is ' ' (keep), '-' (delete old[Old]) or
// '+' (insert new[New]). Old and New are the 0-based positions in the
// old and new sequences immediately before this op.
type Op struct {
	Kind     byte
	Old, New int
}

// steps counts diagonal advances (loop iterations plus snake comparisons)
// of the most recent Diff call. Unexported per spec; read via Steps.
var steps int

// Steps returns the step counter of the most recent Diff call.
func Steps() int { return steps }

// Diff returns a shortest script turning a into b. maxDist caps the edit
// distance; a negative value means no cap. If the distance exceeds maxDist,
// Diff returns ErrTooBig.
func Diff(a, b [][]byte, maxDist int) ([]Op, error) {
	n, m := len(a), len(b)
	steps = 0
	max := n + m
	if maxDist >= 0 && maxDist < max {
		max = maxDist
	}
	off := max + 1
	v := make([]int, 2*max+3)
	var trace [][]int
	found := false
	for d := 0; d <= max && !found; d++ {
		for k := -d; k <= d; k += 2 {
			steps++
			var x int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1] // down: insertion
			} else {
				x = v[off+k-1] + 1 // right: deletion (preferred on ties)
			}
			y := x - k
			for x < n && y < m && bytes.Equal(a[x], b[y]) {
				x, y, steps = x+1, y+1, steps+1
			}
			v[off+k] = x
			if x >= n && y >= m {
				found = true
			}
		}
		if found {
			return backtrack(trace, off, n, m), nil
		}
		trace = append(trace, append([]int(nil), v...))
	}
	return nil, ErrTooBig
}

// backtrack replays the per-D snapshots to recover the script.
func backtrack(trace [][]int, off, n, m int) []Op {
	var rev []Op
	x, y := n, m
	for d := len(trace); d > 0; d-- {
		p := trace[d-1]
		k := x - y
		var px, py int
		if k == -d || (k != d && p[off+k-1] < p[off+k+1]) {
			px, py = p[off+k+1], p[off+k+1]-(k+1) // came from insertion
		} else {
			px, py = p[off+k-1], p[off+k-1]-(k-1) // came from deletion
		}
		for x > px && y > py {
			x, y = x-1, y-1
			rev = append(rev, Op{' ', x, y})
		}
		if x == px {
			y--
			rev = append(rev, Op{'+', x, y})
		} else {
			x--
			rev = append(rev, Op{'-', x, y})
		}
	}
	for x > 0 && y > 0 {
		x, y = x-1, y-1
		rev = append(rev, Op{' ', x, y})
	}
	out := make([]Op, len(rev))
	for i, o := range rev {
		out[len(rev)-1-i] = o
	}
	return out
}

// Distance returns the minimal number of deleted plus inserted lines.
func Distance(a, b [][]byte) (int, error) {
	ops, err := Diff(a, b, -1)
	d := 0
	for _, o := range ops {
		if o.Kind != ' ' {
			d++
		}
	}
	return d, err
}

// Bytes is a convenience wrapper diffing raw byte strings line-wise.
func Bytes(a, b []byte, maxDist int) ([]Op, error) {
	return Diff(lines.Split(a), lines.Split(b), maxDist)
}
