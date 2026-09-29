// Package edit computes a shortest edit script between two line sequences
// with the forward Myers O(ND) algorithm. Ties resolve toward deletions.
package edit

import (
	"errors"

	"ontology/lines"
)

// Kind classifies one edit operation.
type Kind uint8

const (
	Equal Kind = iota
	Delete
	Insert
)

// Op is one edit step against the old sequence.
type Op struct {
	Kind Kind
	Old  lines.Line // valid for Equal, Delete
	New  lines.Line // valid for Equal, Insert
}

// Options bounds a diff. MaxDist<=0 means unlimited.
type Options struct {
	MaxDist int
}

// ErrTooDifferent is returned when more than MaxDist edits are required.
var ErrTooDifferent = errors.New("edit: difference exceeds maximum edit distance")

// lastSteps counts diagonal advances (snake comparisons included) of the
// most recent Diff call.
var lastSteps int

// LastSteps returns the counter of the most recent Diff call.
func LastSteps() int { return lastSteps }

// Diff returns a shortest script transforming a into b.
func Diff(a, b []lines.Line, opts Options) ([]Op, error) {
	n, m := len(a), len(b)
	max := n + m
	if opts.MaxDist > 0 && opts.MaxDist < max {
		max = opts.MaxDist
	}
	lastSteps = 0
	type snap struct{ k, x int }
	trail := []map[int]int{{1: 0}}
	v := map[int]int{1: 0}
	found := false
	d := 0
	for ; d <= max; d++ {
		for k := -d; k <= d; k += 2 {
			down := k == -d || (k != d && v[k-1] < v[k+1])
			x := v[k+1]
			if down {
				x = v[k-1]
			}
			y := x - k
			for x < n && y < m {
				lastSteps++
				if a[x].Content != b[y].Content || a[x].End != b[y].End {
					break
				}
				x++
				y++
			}
			if x < n || y < m {
				lastSteps++ // the mismatching comparison that stopped the snake
			}
			v[k] = x
			if x >= n && y >= m {
				found = true
				break
			}
		}
		if found {
			break
		}
		cp := make(map[int]int, len(v))
		for k, x := range v {
			cp[k] = x
		}
		trail = append(trail, cp)
	}
	if !found {
		return nil, ErrTooDifferent
	}
	return backtrack(a, b, trail, d), nil
}

func backtrack(a, b []lines.Line, trail []map[int]int, d int) []Op {
	n, m := len(a), len(b)
	x, y := n, m
	var ops []Op
	for dd := d; dd > 0; dd-- {
		v := trail[dd-1]
		k := x - y
		down := k == -dd || (k != dd && v[k-1] < v[k+1])
		pk, px := k+1, v[k+1]
		if down {
			pk, px = k-1, v[k-1]
		}
		py := px - pk
		for x > px && y > py {
			ops = append(ops, Op{Kind: Equal, Old: a[x-1], New: b[y-1]})
			x--
			y--
		}
		if down {
			ops = append(ops, Op{Kind: Delete, Old: a[x-1]})
			x--
		} else {
			ops = append(ops, Op{Kind: Insert, New: b[y-1]})
			y--
		}
	}
	for x > 0 {
		ops = append(ops, Op{Kind: Equal, Old: a[x-1], New: b[y-1]})
		x--
		y--
	}
	for i, j := 0, len(ops)-1; i < j; i, j = i+1, j-1 {
		ops[i], ops[j] = ops[j], ops[i]
	}
	return ops
}
