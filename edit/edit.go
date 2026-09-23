// Package edit computes shortest edit scripts between newline-preserving lines.
package edit

import (
	"errors"

	"ontology/lines"
)

// Op identifies one edit step.
type Op int

const (
	Equal Op = iota
	Delete
	Insert
)

// Step is one aligned line pair of the edit script.
type Step struct {
	Kind Op
	A    lines.Line // Equal or Delete
	B    lines.Line // Equal or Insert
}

// ErrTooDifferent reports that the edit distance exceeded the configured limit.
var ErrTooDifferent = errors.New("edit: edit distance exceeds limit")

// compareCount counts diagonal (snake) comparisons of the latest Script call.
var compareCount int

// Count returns the snake comparison count of the most recent Script call.
func Count() int { return compareCount }

func same(x, y lines.Line) bool {
	return string(x.Text) == string(y.Text) && x.CRLF == y.CRLF && x.NL == y.NL
}

// Script returns a shortest edit script (Myers O(ND)). maxD<0 means no limit;
// when the true distance exceeds maxD it returns ErrTooDifferent. On ties the
// path that deletes first (horizontal edge) is chosen, deterministically.
func Script(a, b []lines.Line, maxD int) ([]Step, error) {
	compareCount = 0
	n, m := len(a), len(b)
	off := n + m
	v := make([]int, 2*off+3)
	v[off+1] = 0
	trace := [][]int{}
	foundD := -1
	for d := 0; d <= n+m; d++ {
		if maxD >= 0 && d > maxD {
			return nil, ErrTooDifferent
		}
		snap := make([]int, len(v))
		copy(snap, v)
		trace = append(trace, snap)
		for k := -d; k <= d; k += 2 {
			x := 0
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1] // vertical edge: insert
			} else {
				x = v[off+k-1] + 1 // horizontal edge: delete (preferred on ties)
			}
			y := x - k
			for x < n && y < m {
				compareCount++
				if !same(a[x], b[y]) {
					break
				}
				x, y = x+1, y+1
			}
			v[off+k] = x
			if x >= n && y >= m {
				foundD = d
			}
		}
		if foundD >= 0 {
			break
		}
	}
	return backtrack(a, b, trace, foundD, off), nil
}

func backtrack(a, b []lines.Line, trace [][]int, foundD, off int) []Step {
	x, y := len(a), len(b)
	var rev []Step
	for d := foundD; d > 0; d-- {
		v := trace[d]
		k := x - y
		prevK := k + 1
		if !(k == -d || (k != d && v[off+k-1] < v[off+k+1])) {
			prevK = k - 1
		}
		px := v[off+prevK]
		py := px - prevK
		for x > px && y > py {
			rev = append(rev, Step{Kind: Equal, A: a[x-1], B: b[y-1]})
			x, y = x-1, y-1
		}
		if x == px {
			rev = append(rev, Step{Kind: Insert, B: b[y-1]})
			y--
		} else {
			rev = append(rev, Step{Kind: Delete, A: a[x-1]})
			x--
		}
	}
	for x > 0 && y > 0 {
		rev = append(rev, Step{Kind: Equal, A: a[x-1], B: b[y-1]})
		x, y = x-1, y-1
	}
	out := make([]Step, len(rev))
	for i, s := range rev {
		out[len(rev)-1-i] = s
	}
	return out
}
