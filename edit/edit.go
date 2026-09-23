// Package edit computes a shortest edit script between two line
// sequences using the Myers O(ND) algorithm.
package edit

import (
	"errors"

	"ontology/lines"
)

// ErrTooLarge reports that the edit distance exceeded Differ.MaxDist.
var ErrTooLarge = errors.New("edit: edit distance exceeds limit")

// Op is one edit operation kind.
type Op int

const (
Equal  Op = iota // line present in both
Delete           // line removed from A
Insert           // line added from B
)

// Step is one element of an edit script. A is set for Equal/Delete,
// B is set for Equal/Insert; Ai/Bi are 0-based indexes in the inputs.
type Step struct {
	Op Op
	A  *lines.Line
	B  *lines.Line
	Ai int
	Bi int
}

// Script is a shortest alignment of A and B.
type Script struct {
	Steps []Step
	Dist  int // number of Delete + Insert steps
}

// Differ computes scripts. MaxDist <= 0 means no distance limit.
// The tie rule is delete-before-insert (see DESIGN.md §3).
type Differ struct {
	MaxDist int
	steps   int64
}

// Steps returns the diagonal-advance work count of the last Diff call,
// including every per-line snake comparison.
func (d *Differ) Steps() int64 { return d.steps }

// Diff returns a shortest edit script between a and b.
func (d *Differ) Diff(a, b []lines.Line) (*Script, error) {
	n, m := len(a), len(b)
	d.steps = 0
	snaps := []map[int]int{}
	v := map[int]int{1: 0}
	found := false
	max := d.MaxDist
	if max <= 0 {
		max = n + m
	}
loop:
	for D := 0; D <= max; D++ {
		for k := -D; k <= D; k += 2 {
			var x int
			if k == -D || (k != D && v[k-1] < v[k+1]) {
				x = v[k+1] // move down: delete
			} else {
				x = v[k-1] + 1 // move right: insert
			}
			y := x - k
			for x < n && y < m && eq(&a[x], &b[y]) {
				d.steps++
				x, y = x+1, y+1
			}
			v[k] = x
			if x >= n && y >= m {
				snap := make(map[int]int, len(v))
				for kk, vv := range v {
					snap[kk] = vv
				}
				snaps = append(snaps, snap)
				found = true
				break loop
			}
		}
		snap := make(map[int]int, len(v))
		for kk, vv := range v {
			snap[kk] = vv
		}
		snaps = append(snaps, snap)
	}
	if !found {
		return nil, ErrTooLarge
	}
	steps := d.backtrack(a, b, snaps)
	dist := 0
	for i := range steps {
		if steps[i].Op != Equal {
			dist++
		}
	}
	return &Script{Steps: steps, Dist: dist}, nil
}

func (d *Differ) backtrack(a, b []lines.Line, snaps []map[int]int) []Step {
	n, m := len(a), len(b)
	out := []Step{}
	x, y := n, m
	for D := len(snaps) - 1; D >= 0; D-- {
		v := snaps[D]
		k := x - y
		down := k == -D || (k != D && v[k-1] < v[k+1])
		var pk int
		if down {
			pk = k + 1
		} else {
			pk = k - 1
		}
		px, py := v[pk], v[pk]-pk
		for x > px && y > py {
			i, j := x-1, y-1
			out = append(out, Step{Op: Equal, A: &a[i], B: &b[j], Ai: i, Bi: j})
			x, y = i, j
		}
		if D > 0 {
			if down {
				out = append(out, Step{Op: Delete, A: &a[x-1], Ai: x - 1})
				x--
			} else {
				out = append(out, Step{Op: Insert, B: &b[y-1], Bi: y - 1})
				y--
			}
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func eq(a, b *lines.Line) bool {
	d.steps++
	return a.Content == b.Content
}
