// Package edit computes a shortest edit script between two line sequences
// using the forward Myers O(ND) algorithm. Ties always prefer a deletion.
package edit

import (
	"errors"

	"ontology/lines"
)

// Op is one edit operation.
type Op int

const (
	Equal Op = iota // context line, Index shared by both sides
	Delete          // line removed from A
	Insert          // line added to B
)

// Step is one operation. AIndex/BIndex index into the relevant side.
type Step struct {
	Op     Op
	AIndex int
	BIndex int
}

// ErrTooDifferent is returned when the edit distance exceeds the configured cap.
var ErrTooDifferent = errors.New("edit: files differ by more than the configured limit")

// counter records diagonal advance steps (each snake line-compare and each
// edge move) made by the most recent Diff call. It is intentionally unexported.
var counter int64

// Steps returns the counter value (diagonal advance steps of the last Diff).
func Steps() int64 { return counter }

// Diff returns a shortest edit script turning a into b. maxD caps the edit
// distance; maxD<0 means unlimited.
func Diff(a, b []lines.Line, maxD int) ([]Step, error) {
	n, m := len(a), len(b)
	counter = 0
	v := map[int]int{1: 0}
	trace := []map[int]int{}
	limit := n + m
	if maxD >= 0 && maxD < limit {
		limit = maxD
	}
	found := false
	for d := 0; d <= limit; d++ {
		tv := make(map[int]int, d+1)
		for k := -d; k <= d; k += 2 {
			x := 0
			if k == -d || (k != d && v[k-1] < v[k+1]) {
				x = v[k+1] // move right (insert)
			} else {
				x = v[k-1] + 1 // move down (delete) — preferred on ties
			}
			y := x - k
			counter++ // one horizontal/vertical edge
			for x < n && y < m && a[x] == b[y] {
				x, y = x+1, y+1
				counter++ // one snake diagonal comparison
			}
			v[k] = x
			tv[k] = x
			if x >= n && y >= m {
				found = true
			}
		}
		trace = append(trace, tv)
		if found {
			return backtrack(a, b, trace), nil
		}
	}
	return nil, ErrTooDifferent
}

func backtrack(a, b []lines.Line, trace []map[int]int) []Step {
	x, y := len(a), len(b)
	ops := []Step{}
	for d := len(trace) - 1; d >= 0; d-- {
		v := trace[d]
		for x > 0 || y > 0 {
			k := x - y
			prevK := k + 1 // right (insert)
			if k == -d || (k != d && v[k-1] >= v[k+1]) {
				prevK = k - 1 // down (delete), preferred tie
			}
			px := v[prevK]
			if d == 0 {
				px = 0
			}
			py := px - prevK
			for x > px && y > py {
				ops = append(ops, Step{Op: Equal, AIndex: x - 1, BIndex: y - 1})
				x, y = x-1, y-1
			}
			if d > 0 && x > px {
				ops = append(ops, Step{Op: Delete, AIndex: x - 1})
				x--
			} else if d > 0 && y > py {
				ops = append(ops, Step{Op: Insert, BIndex: y - 1})
				y--
			}
			break // move to previous d-layer
		}
	}
	for i, j := 0, len(ops)-1; i < j; i, j = i+1, j-1 {
		ops[i], ops[j] = ops[j], ops[i]
	}
	return ops
}
