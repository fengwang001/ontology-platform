// Package edit computes shortest edit scripts between line sequences using
// Myers' O(ND) algorithm.
package edit

import (
	"errors"

	"ontology/lines"
)

// Op is the kind of an edit step or segment.
type Op uint8

const (
	// Equal is an unchanged line present in both inputs.
	Equal Op = iota
	// Delete is a line removed from a.
	Delete
	// Insert is a line added to b.
	Insert
)

// Segment is a maximal run of equal-kind edit steps.
type Segment struct {
	Op    Op
	Lines []lines.Line
}

// ErrTooDifferent reports that the edit distance exceeded MaxEdits.
var ErrTooDifferent = errors.New("edit: edit distance exceeds configured limit")

// Differ computes scripts. Steps counts diagonal progress of the most recent
// Diff call, including every per-line snake comparison.
type Differ struct {
	// MaxEdits caps deletions+insertions; 0 means unlimited.
	MaxEdits int
	Steps    int64
}

// Diff returns a shortest script turning a into b. Ties always prefer a
// deletion over an insertion, so within a change deletions are emitted first.
func (d *Differ) Diff(a, b []lines.Line) ([]Segment, error) {
	d.Steps = 0
	n, m := len(a), len(b)
	v := map[int]int{1: 0}
	var trace []map[int]int
	found := false
	depth := 0
	for dd := 0; dd <= n+m; dd++ {
		if d.MaxEdits > 0 && dd > d.MaxEdits {
			return nil, ErrTooDifferent
		}
		for k := -dd; k <= dd; k += 2 {
			var x int
			if k == -dd || (k != dd && v[k-1] < v[k+1]) {
				x = v[k+1]
			} else {
				x = v[k-1] + 1
			}
			y := x - k
			for x < n && y < m {
				d.Steps++
				if !a[x].Equal(b[y]) {
					break
				}
				x, y = x+1, y+1
				d.Steps++
			}
			v[k] = x
			if x == n && y == m {
				found, depth = true, dd
			}
		}
		snap := make(map[int]int, len(v))
		for k, x := range v {
			snap[k] = x
		}
		trace = append(trace, snap)
		if found {
			break
		}
	}
	type step struct {
		op   Op
		line lines.Line
	}
	var rev []step
	x, y := n, m
	for dd := depth; dd >= 0; dd-- {
		if dd == 0 {
			for x > 0 {
				rev = append(rev, step{Equal, a[x-1]})
				x, y = x-1, y-1
			}
			break
		}
		vv := trace[dd]
		k := x - y
		var pk int
		if k == -dd || (k != dd && vv[k-1] < vv[k+1]) {
			pk = k + 1
		} else {
			pk = k - 1
		}
		px, py := vv[pk], vv[pk]-pk
		for x > px && y > py {
			rev = append(rev, step{Equal, a[x-1]})
			x, y = x-1, y-1
		}
		if x > px {
			rev = append(rev, step{Delete, a[x-1]})
			x--
		} else {
			rev = append(rev, step{Insert, b[y-1]})
			y--
		}
	}
	var segs []Segment
	for i := len(rev) - 1; i >= 0; i-- {
		s := rev[i]
		if len(segs) == 0 || segs[len(segs)-1].Op != s.op {
			segs = append(segs, Segment{Op: s.op})
		}
		sg := &segs[len(segs)-1]
		sg.Lines = append(sg.Lines, s.line)
	}
	return segs, nil
}

// Distance returns deletions plus insertions of the script.
func Distance(segs []Segment) int {
	total := 0
	for _, s := range segs {
		if s.Op != Equal {
			total += len(s.Lines)
		}
	}
	return total
}
