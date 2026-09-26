// Package edit computes a shortest edit script between two line slices with
// Myers' O(ND) algorithm. Ties are broken in favour of deletions.
package edit

import (
	"errors"

	"ontology/lines"
)

// Kind classifies one script operation.
type Kind uint8

const (
	Equal Kind = iota
	Del
	Ins
)

// Op is one script step. A/B index the logical content line (0-based) in
// each input; only the relevant index is set per kind.
type Op struct {
	Kind  Kind
	A, B  int
	Lines lines.Line
}

// ErrTooDifferent is returned when the edit distance exceeds the configured
// limit. It is the sentinel for the "差异过大" error class.
var ErrTooDifferent = errors.New("edit: differences exceed limit")

// Script holds a shortest edit script and diagnostics of the last Diff run.
type Script struct {
	Ops []Op
	D   int // edit distance = deletions + insertions
	// Steps counts diagonal advances during the last run, including every
	// snake comparison, per package complexity contract.
	Steps int
}

type eqLine struct {
	s string
	n string
}

// Diff runs Myers. maxD < 0 means unlimited. Comparison is on logical
// content only (newline differences are represented separately by hunks).
func Diff(a, b []lines.Line, maxD int) (Script, error) {
	n, m := len(a), len(b)
	ak := make([]eqLine, n)
	bk := make([]eqLine, m)
	for i, l := range a {
		ak[i] = eqLine{string(l.Content), string(l.NL)}
	}
	for i, l := range b {
		bk[i] = eqLine{string(l.Content), string(l.NL)}
	}
	eq := func(x, y int) bool { return x < n && y < m && ak[x] == bk[y] }

	steps := 0
	max := n + m + 1
	if maxD < 0 || maxD > max {
		maxD = max
	}
	v := make([]int, 2*max+1)
	off := max
	type snap map[int]int
	trace := []snap{}

	found := false
	var d int
	for d = 0; d <= maxD && !found; d++ {
		snap := snap{}
		for k := -d; k <= d; k += 2 {
			var x int
			switch {
			case k == -d:
				x = v[k-1+off] + 1
			case k == d:
				x = v[k+1+off]
			case v[k-1+off] < v[k+1+off]:
				x = v[k-1+off] + 1
			default:
				x = v[k+1+off]
			}
			y := x - k
			for eq(x, y) {
				x++
				y++
				steps++
			}
			steps++
			v[k+off] = x
			snap[k] = x
			if x >= n && y >= m {
				found = true
			}
		}
		trace = append(trace, snap)
	}
	if !found {
		return Script{Steps: steps}, ErrTooDifferent
	}

	ops := []Op{}
	x, y := n, m
	for di := d - 1; di >= 0; di-- {
		snap := trace[di]
		k := x - y
		var pk int
		switch {
		case k == -di:
			pk = k + 1
		case k == di:
			pk = k - 1
		default:
			if snap[k-1] < snap[k+1] {
				pk = k + 1
			} else {
				pk = k - 1
			}
		}
		px := snap[pk]
		py := px - pk
		for x > px && y > py {
			ops = append(ops, Op{Equal, x - 1, y - 1, a[x-1]})
			x--
			y--
		}
		if di > 0 {
			if x > px {
				ops = append(ops, Op{Del, x - 1, y, a[x-1]})
				x--
			} else {
				ops = append(ops, Op{Ins, x, y - 1, b[y-1]})
				y--
			}
		}
	}
	for x > 0 && y > 0 {
		ops = append(ops, Op{Equal, x - 1, y - 1, a[x-1]})
		x--
		y--
	}
	for i, j := 0, len(ops)-1; i < j; i, j = i+1, j-1 {
		ops[i], ops[j] = ops[j], ops[i]
	}
	dels, ins := 0, 0
	for _, o := range ops {
		if o.Kind == Del {
			dels++
		}
		if o.Kind == Ins {
			ins++
		}
	}
	return Script{Ops: ops, D: dels + ins, Steps: steps}, nil
}
