// Package edit computes a shortest edit script (Myers O(ND)) with a distance
// cap and a diagonal-step counter. Depends only on package lines.
package edit

import (
	"errors"

	"ontology/lines"
)

// ErrTooDifferent is returned when the edit distance exceeds MaxDistance.
var ErrTooDifferent = errors.New("edit: files differ by more than the allowed distance")

// Kind enumerates elementary operations.
type Kind uint8

const (
	Equal  Kind = iota // line kept from both sides
	Delete             // line only in A
	Insert             // line only in B
)

// Op is one elementary edit.
type Op struct {
	Kind Kind
	A    lines.Line // valid for Equal and Delete
	B    lines.Line // valid for Equal and Insert
}

// Script is a shortest edit script plus the measured distance and work count.
type Script struct {
	Ops      []Op
	Distance int // number of deletions plus insertions
	Steps    int // internal counter: total diagonal advances incl. snakes
}

// Options configures Diff.
type Options struct {
	MaxDistance int // <= 0 means unlimited
}

type frame map[int]int // k -> furthest x at one d layer

// Diff returns a shortest script from a to b. Ties resolve to deletion first.
// LastSteps exposes the internal counter from the latest Diff call.
var lastSteps int

// LastSteps returns the internal counter from the latest Diff invocation.
func LastSteps() int { return lastSteps }

func Diff(a, b []lines.Line, opts Options) (Script, error) {
	n, m := len(a), len(b)
	maxD := n + m
	if opts.MaxDistance > 0 {
		maxD = opts.MaxDistance
	}
	v := map[int]int{1: 0}
	trace := make([]frame, 0, 8)
	steps := 0
	var endX int
	found := false
	for d := 0; d <= maxD; d++ {
		snap := frame{}
		for k := -d; k <= d; k += 2 {
			var x int
			switch {
			case k == -d:
				x = v[k+1]
			case k == d:
				x = v[k-1] + 1
			default:
				down, right := v[k+1], v[k-1]+1
				if down >= right { // deletion first on ties
					x = down
				} else {
					x = right
				}
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
				steps++
			}
			v[k] = x
			snap[k] = x
			if x == n && y == m {
				endX = x
				found = true
			}
		}
		trace = append(trace, snap)
		if found {
			lastSteps = steps
			return backtrack(a, b, trace, endX, steps), nil
		}
	}
	lastSteps = steps
	return Script{Steps: steps}, ErrTooDifferent
}

func backtrack(a, b []lines.Line, trace []frame, x, steps int) Script {
	y, ops := len(b), make([]Op, 0, len(a)+len(b))
	for d := len(trace) - 1; d > 0; d-- {
		v := trace[d-1]
		k := x - y
		var pk int
		switch {
		case k == -d:
			pk = k + 1
		case k == d:
			pk = k - 1
		default: // mirrors the forward delete-first tie rule
			if v[k+1] >= v[k-1]+1 {
				pk = k + 1
			} else {
				pk = k - 1
			}
		}
		px := v[pk]
		py := px - pk
		for x > px && y > py {
			ops = append(ops, Op{Kind: Equal, A: a[x-1], B: b[y-1]})
			x--
			y--
		}
		if x == px {
			ops = append(ops, Op{Kind: Insert, B: b[y-1]})
			y--
		} else {
			ops = append(ops, Op{Kind: Delete, A: a[x-1]})
			x--
		}
	}
	for x > 0 && y > 0 && a[x-1] == b[y-1] {
		ops = append(ops, Op{Kind: Equal, A: a[x-1], B: b[y-1]})
		x--
		y--
	}
	for i, j := 0, len(ops)-1; i < j; i, j = i+1, j-1 {
		ops[i], ops[j] = ops[j], ops[i]
	}
	dist := 0
	for _, op := range ops {
		if op.Kind != Equal {
			dist++
		}
	}
	return Script{Ops: ops, Distance: dist, Steps: steps}
}
