// Package edit computes shortest edit scripts between two line sequences
// using Myers' O(ND) algorithm with a configurable distance cap.
package edit

import (
	"errors"
	"slices"
	"sync/atomic"

	"ontology/lines"
)

// ErrTooDifferent is returned when the edit distance exceeds the cap.
var ErrTooDifferent = errors.New("edit: distance exceeds maximum")

// Op is one edit: delete a[A] (Del) or insert b[B] at a-position A.
type Op struct {
	Del  bool
	A, B int
}

// Script is a shortest sequence of ops turning a into b, in file order.
type Script []Op

// steps counts diagonal advances (greedy steps plus per-line snake
// comparisons) of the most recent Diff. Unexported per design; read it
// through LastSteps. Atomic so concurrent Diffs stay race-free.
var steps atomic.Int64

// LastSteps returns the step counter of the most recent Diff call.
func LastSteps() int64 { return steps.Load() }

// Diff returns a shortest edit script turning a into b. maxD caps the edit
// distance; negative means unlimited. Ties between equally short paths are
// resolved in favour of deletion (see DESIGN.md §3).
func Diff(a, b [][]byte, maxD int) (Script, error) {
	n, m := len(a), len(b)
	max := n + m
	if maxD >= 0 && maxD < max {
		max = maxD
	}
	off := max + 1
	v := make([]int, 2*max+3)
	var trace [][]int
	var count int64
	found := -1
loop:
	for d := 0; d <= max; d++ {
		for k := -d; k <= d; k += 2 {
			count++
			var x int
			if k == -d || (k != d && v[k-1+off] < v[k+1+off]) {
				x = v[k+1+off] // down: insertion
			} else {
				x = v[k-1+off] + 1 // right: deletion (wins ties)
			}
			y := x - k
			for x < n && y < m && lines.Equal(a[x], b[y]) {
				x, y, count = x+1, y+1, count+1
			}
			v[k+off] = x
			if x >= n && y >= m {
				found = d
				break loop
			}
		}
		trace = append(trace, slices.Clone(v))
	}
	steps.Store(count)
	if found < 0 {
		return nil, ErrTooDifferent
	}
	return backtrack(a, b, trace, off, found), nil
}

// backtrack replays the stored frontiers to recover the script, in reverse.
func backtrack(a, b [][]byte, trace [][]int, off, D int) Script {
	var rev []Op
	x, y := len(a), len(b)
	for d := D; d > 0; d-- {
		v := trace[d-1]
		k := x - y
		down := k == -d || (k != d && v[k-1+off] < v[k+1+off])
		pk := k - 1
		if down {
			pk = k + 1
		}
		px, py := v[pk+off], v[pk+off]-pk
		mx, my := px+1, py // post-move point; down move overrides below
		if down {
			mx, my = px, py+1
		}
		for x > mx && y > my { // follow the snake back
			x, y = x-1, y-1
		}
		if down {
			y--
			rev = append(rev, Op{Del: false, A: x, B: y})
		} else {
			x--
			rev = append(rev, Op{Del: true, A: x, B: y})
		}
	}
	slices.Reverse(rev)
	return Script(rev)
}
