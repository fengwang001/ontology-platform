// Package edit computes a shortest edit script between two line sequences
// using the Myers O(ND) algorithm, with a configurable distance limit.
package edit

import (
	"errors"

	"ontology/lines"
)

// Op is one edit operation kind.
type Op uint8

const (
	// Equal is a line present in both A and B at the same position.
	Equal Op = iota
	// Delete removes a line from A.
	Delete
	// Insert adds a line from B.
	Insert
)

// Item is one step of an edit script: an Equal, Delete (from A) or Insert
// (from B) line. Scripts are emitted in document order.
type Item struct {
	Op   Op
	Line lines.Line
}

// ErrTooDifferent reports that the edit distance exceeded MaxEdits.
var ErrTooDifferent = errors.New("edit: difference exceeds limit")

// Options configures a Diff. MaxEdits<=0 means unlimited.
type Options struct {
	MaxEdits int
}

// Steps is the unexported-budget counter of diagonal moves performed by the
// most recent Diff, counting every snake line comparison.
var Steps int

// Diff returns a shortest script transforming a into b. Ties are resolved
// in favor of deletions (see DESIGN.md rule 3).
func Diff(a, b []lines.Line, opts Options) ([]Item, error) {
	n, m := len(a), len(b)
	max := n + m
	limit := max
	if opts.MaxEdits > 0 && opts.MaxEdits < limit {
		limit = opts.MaxEdits
	}
	Steps = 0
	off := max + 1
	v := make([]int, 2*max+3)
	trace := [][]int{}
	found := false
	d := 0
	for ; d <= limit; d++ {
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1] // down: prefer deletion on ties
			} else {
				x = v[off+k-1] + 1 // right: insertion
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x, y = x+1, y+1
				Steps++
			}
			v[off+k] = x
			if x >= n && y >= m {
				found = true
			}
		}
		snap := make([]int, len(v))
		copy(snap, v)
		trace = append(trace, snap)
		if found {
			break
		}
	}
	if !found {
		return nil, ErrTooDifferent
	}
	x, y := n, m
	var rev []Item
	for dd := d; dd > 0; dd-- {
		prev := trace[dd-1]
		k := x - y
		pk := k - 1
		if k == -dd || (k != dd && prev[off+k-1] < prev[off+k+1]) {
			pk = k + 1
		}
		px, py := prev[off+pk], prev[off+pk]-pk
		for x > px && y > py {
			rev = append(rev, Item{Op: Equal, Line: a[x-1]})
			x, y = x-1, y-1
		}
		if x == px {
			rev = append(rev, Item{Op: Insert, Line: b[py]})
			y = py
		} else {
			rev = append(rev, Item{Op: Delete, Line: a[px]})
			x = px
		}
	}
	for x > 0 {
		rev = append(rev, Item{Op: Equal, Line: a[x-1]})
		x--
	}
	out := make([]Item, len(rev))
	for i, it := range rev {
		out[len(rev)-1-i] = it
	}
	return out, nil
}
