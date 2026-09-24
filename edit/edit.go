// Package edit computes a shortest edit script between two line sequences
// using Myers' O(ND) algorithm. Ties are resolved in favor of deletions.
package edit

import (
	"errors"

	"ontology/lines"
)

// Kind is one edit operation.
type Kind uint8

const (
	Equal Kind = iota // common line, kept in place
	Delete            // line present only in A
	Insert            // line present only in B
)

// Op is one step of the shortest edit script.
type Op struct {
	Kind Kind
	A    lines.Line // meaningful for Equal and Delete
	B    lines.Line // meaningful for Equal and Insert
}

// ErrTooDifferent reports that the edit distance exceeded MaxDistance.
var ErrTooDifferent = errors.New("edit: difference exceeds configured maximum distance")

// Script is a shortest edit script plus diagnostics.
type Script struct {
	Ops    []Op
	Delete int // number of Delete ops
	Insert int // number of Insert ops
	Steps  int // diagonal-forward steps charged by the most recent Diff
}

// steps is the package-wide counter required by the complexity contract.
var steps int

// LastSteps returns the counter value produced by the most recent Diff call.
func LastSteps() int { return steps }

// Diff computes a shortest edit script. maxDistance<=0 means unbounded; when
// the true distance is larger, ErrTooDifferent is returned. On every path
// the counter charges one step per diagonal advance (each snake line
// comparison included), so Steps stays in O((N+M)(D+1)).
func Diff(a, b []lines.Line, maxDistance int) (*Script, error) {
	steps = 0
	n, m := len(a), len(b)
	maxD := n + m
	if maxDistance > 0 && maxDistance < maxD {
		maxD = maxDistance
	}
	v := make(map[int]int)
	trace := make([]map[int]int, 0, maxD+1)
	found := false
	for d := 0; d <= maxD; d++ {
		for k := -d; k <= d; k += 2 {
			var x int
			switch {
			case k == -d:
				x = v[k+1]
			case k == d:
				x = v[k-1] + 1
			default:
				down, right := v[k+1], v[k-1]+1
				if down >= right { // prefer the down (delete) move on ties
					x = down
				} else {
					x = right
				}
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				steps++
				x++
				y++
			}
			if x < n || y < m {
				steps++
			}
			v[k] = x
			if x >= n && y >= m {
				found = true
			}
		}
		snap := make(map[int]int, len(v))
		for k, x := range v {
			snap[k] = x
		}
		trace = append(trace, snap)
		if found {
			s := backtrack(a, b, trace)
			return s, nil
		}
	}
	return nil, ErrTooDifferent
}

func backtrack(a, b []lines.Line, trace []map[int]int) *Script {
	x, y := len(a), len(b)
	var ops []Op
	s := &Script{}
	for d := len(trace) - 1; d >= 0; d-- {
		v := trace[d]
		k := x - y
		var pk int
		switch {
		case k == -d:
			pk = k + 1
		case k == d:
			pk = k - 1
		default:
			if v[k+1] >= v[k-1]+1 { // same deletion-first tie rule
				pk = k + 1
			} else {
				pk = k - 1
			}
		}
		px, py := v[pk], v[pk]-pk
		for x > px && y > py {
			ops = append(ops, Op{Kind: Equal, A: a[x-1], B: b[y-1]})
			x--
			y--
		}
		if d > 0 {
			switch {
			case x == px:
				ops = append(ops, Op{Kind: Insert, B: b[y-1]})
				y--
				s.Insert++
			case y == py:
				ops = append(ops, Op{Kind: Delete, A: a[x-1]})
				x--
				s.Delete++
			default:
				ops = append(ops, Op{Kind: Delete, A: a[x-1]})
				x--
				s.Delete++
				ops = append(ops, Op{Kind: Insert, B: b[y-1]})
				y--
				s.Insert++
			}
		}
	}
	for i, j := 0, len(ops)-1; i < j; i, j = i+1, j-1 {
		ops[i], ops[j] = ops[j], ops[i]
	}
	s.Ops = ops
	s.Steps = steps
	return s
}
