// Package edit computes a shortest edit script between two line sequences
// using the Myers O(ND) algorithm.
package edit

import (
	"errors"

	"ontology/lines"
)

// Op is one edit operation.
type Op uint8

const (
	Equal Op = iota
	Delete
	Insert
)

// Step is one element of an edit script.
type Step struct {
	Op   Op
	A    lines.Line // old side: Equal, Delete
	B    lines.Line // new side: Equal, Insert
}

// ErrTooDifferent is returned when the edit distance exceeds the configured cap.
var ErrTooDifferent = errors.New("edit: difference exceeds distance cap")

// Script is a shortest edit script with diagnostics.
type Script struct {
	Steps []Step
	Dist  int // deletions + insertions
	steps int // diagonal advances (snake comparisons included), last Diff
}

// Counts reports the number of diagonal advances performed by the most recent
// Diff, including every per-line snake comparison.
func (s *Script) Counts() int { return s.steps }

// Diff returns a shortest script transforming a into b. maxD caps the edit
// distance; zero means unlimited. Ties resolve in favor of deletions.
func Diff(a, b []lines.Line, maxD int) (*Script, error) {
	n, m := len(a), len(b)
	v := make([]int, 2*(n+m)+3)
	off := n + m + 1
	var trace []map[int]int
	count := 0
	found := false
	for d := 0; d <= n+m; d++ {
		if maxD > 0 && d > maxD {
			return &Script{steps: count}, ErrTooDifferent
		}
		row := make(map[int]int, 2*d+1)
		for k := -d; k <= d; k += 2 {
			var x int
			switch {
			case k == -d:
				x = v[k+1+off]
			case k == d:
				x = v[k-1+off] + 1
			default:
				down, right := v[k+1+off], v[k-1+off]
				if down >= right { // down (delete) preferred on ties
					x = down + 1
				} else {
					x = right
				}
			}
			y := x - k
			for x < n && y < m && same(a[x], b[y]) {
				x++
				y++
				count++
			}
			v[k+off] = x
			row[k] = x
			if x == n && y == m {
				found = true
			}
		}
		trace = append(trace, row)
		if found {
			break
		}
	}
	// Backtrack through saved diagonals; on ties prefer the down (delete) edge.
	x, y := n, m
	var rev []Step
	for d := len(trace) - 1; d >= 0; d-- {
		k := x - y
		for x > 0 && y > 0 && same(a[x-1], b[y-1]) {
			rev = append(rev, Step{Op: Equal, A: a[x-1], B: b[y-1]})
			x--
			y--
		}
		if d == 0 {
			break
		}
		prev := trace[d-1]
		dn, hasDn := prev[k+1]
		rt, hasRt := prev[k-1]
		var pk int
		switch {
		case !hasDn:
			pk = k - 1
		case !hasRt:
			pk = k + 1
		case dn >= rt:
			pk = k + 1
		default:
			pk = k - 1
		}
		nx := prev[pk]
		if pk == k+1 { // came down: deleted a[nx]
			rev = append(rev, Step{Op: Delete, A: a[nx]})
			x = nx
			y = x - pk
		} else { // came right: insertion of b[y-1]
			y = nx - pk
			rev = append(rev, Step{Op: Insert, B: b[y]})
			x = nx
		}
	}
	out := make([]Step, len(rev))
	for i, s := range rev {
		out[len(rev)-1-i] = s
	}
	dist := 0
	for _, s := range out {
		if s.Op != Equal {
			dist++
		}
	}
	return &Script{Steps: out, Dist: dist, steps: count}, nil
}

func same(p, q lines.Line) bool {
	return p.Text == q.Text && p.End == q.End
}
