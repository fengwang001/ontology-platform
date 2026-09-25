// Package edit computes a shortest edit script between two line sequences.
package edit

import (
	"errors"
	"sync/atomic"

	"ontology/lines"
)

// Op is one edit operation kind.
type Op uint8

const (
	Equal Op = iota
	Delete
	Insert
)

// Step is one aligned entry of an edit script.
type Step struct {
	Op Op
	A  lines.Line // present for Equal/Delete
	B  lines.Line // present for Equal/Insert
}

// ErrTooDifferent reports that the edit distance exceeded the configured cap.
var ErrTooDifferent = errors.New("edit: difference exceeds max distance")

// Options configures a Diff.
type Options struct {
	MaxDistance int // <= 0 means unlimited
}

var snakeCount atomic.Int64

// Diff returns a shortest script (delete+insert minimal) from a to b.
// Ties on equal-length paths resolve delete-first (see DESIGN.md §3).
func Diff(a, b []lines.Line, opts Options) ([]Step, error) {
	snakeCount.Store(0)
	n, m := len(a), len(b)
	off := n + m + 1
	v := make([]int, 2*off+1)
	var trace [][]int
	var ex, ey int
	found := false
loop:
	for d := 0; d <= n+m; d++ {
		if opts.MaxDistance > 0 && d > opts.MaxDistance {
			return nil, ErrTooDifferent
		}
		for k := -d; k <= d; k += 2 {
			x := next(v, off, k, d)
			y := x - k
			for x < n && y < m && eq(a[x], b[y]) {
				snakeCount.Add(1)
				x++
				y++
			}
			if x < n && y < m {
				snakeCount.Add(1) // one failed comparison on this diagonal
			}
			v[off+k] = x
			if x >= n && y >= m {
				ex, ey, found = x, y, true
				break loop
			}
		}
		cp := make([]int, len(v))
		copy(cp, v)
		trace = append(trace, cp)
	}
	if !found {
		return nil, ErrTooDifferent
	}
	return backtrack(a, b, trace, off, ex, ey), nil
}

func next(v []int, off, k, d int) int {
	if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
		return v[off+k+1] // insert-first on the lower half
	}
	return v[off+k-1] + 1 // delete-first on the upper half (ties)
}

func backtrack(a, b []lines.Line, trace [][]int, off, ex, ey int) []Step {
	var rev []Step
	x, y := ex, ey
	for d := len(trace); d > 0; d-- {
		v := trace[d-1]
		k := x - y
		pk := k - 1
		if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
			pk = k + 1
		}
		px, py := v[off+pk], v[off+pk]-pk
		for (pk == k-1 && x > px+1) || (pk == k+1 && y > py+1) {
			rev = append(rev, Step{Op: Equal, A: a[x-1], B: b[y-1]})
			x--
			y--
		}
		if x == px {
			rev = append(rev, Step{Op: Insert, B: b[y-1]})
			y--
		} else {
			rev = append(rev, Step{Op: Delete, A: a[x-1]})
			x--
		}
	}
	for x > 0 || y > 0 {
		st := Step{Op: Equal}
		if x > 0 {
			st.A = a[x-1]
			x--
		}
		if y > 0 {
			st.B = b[y-1]
			y--
		}
		rev = append(rev, st)
	}
	out := make([]Step, len(rev))
	for i := range rev {
		out[i] = rev[len(rev)-1-i]
	}
	return out
}

// Distance is the number of delete+insert steps in s.
func Distance(s []Step) int {
	d := 0
	for _, st := range s {
		if st.Op != Equal {
			d++
		}
	}
	return d
}

// SnakeSteps reports diagonal-advance comparisons made by the last Diff.
func SnakeSteps() int64 { return snakeCount.Load() }

func eq(x, y lines.Line) bool {
	return string(x.Text) == string(y.Text) && string(x.NL) == string(y.NL)
}
