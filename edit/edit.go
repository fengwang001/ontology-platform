// Package edit computes a shortest edit script between two line sequences
// using the Myers O(ND) algorithm. On ties it prefers deletions.
package edit

import (
	"errors"

	"ontology/lines"
)

// ErrTooDifferent is returned when the edit distance exceeds the configured cap.
var ErrTooDifferent = errors.New("edit: difference exceeds maximum edit distance")

// Kind is an edit operation kind.
type Kind uint8

const (
	Equal Kind = iota
	Delete
	Insert
)

// Op is one edit operation. AI indexes the old line for Equal/Delete, else -1;
// BI indexes the new line for Equal/Insert, else -1.
type Op struct {
	Kind Kind
	AI   int
	BI   int
}

// Differ holds configuration and the most recent run's step counter.
type Differ struct {
	MaxDist int // cap on delete+insert count; 0 means unlimited.
	steps   int // diagonal advances incl. per-line snake comparisons
}

// NewDiffer returns a Differ with the given edit-distance cap (0 = unlimited).
func NewDiffer(maxDist int) *Differ { return &Differ{MaxDist: maxDist} }

// Steps returns the counter from the most recent Diff call.
func (d *Differ) Steps() int { return d.steps }

// Diff computes a shortest edit script from a to b.
func (d *Differ) Diff(a, b []lines.Line) ([]Op, error) {
	n, m := len(a), len(b)
	d.steps = 0
	maxD := n + m
	if d.MaxDist > 0 && d.MaxDist < maxD {
		maxD = d.MaxDist
	}
	v := map[int]int{}
	trace := make([]map[int]int, 0, maxD+1)
	for dd := 0; dd <= maxD; dd++ {
		snap := map[int]int{}
		for k := -dd; k <= dd; k += 2 {
			var x int
			if k == -dd || (k != dd && v[k-1] < v[k+1]) {
				x = v[k+1] // down = delete; ties take this branch
			} else {
				x = v[k-1] + 1 // right = insert
			}
			y := x - k
			d.steps++ // count the single non-snake advance
			for x < n && y < m && bytesEqual(a[x].Raw, b[y].Raw) {
				x++
				y++
				d.steps++ // count each snake line comparison
			}
			v[k] = x
			snap[k] = x
			if x >= n && y >= m {
				trace = append(trace, snap)
				return backtrack(a, b, trace, dd), nil
			}
		}
		trace = append(trace, snap)
	}
	return nil, ErrTooDifferent
}

// backtrack rebuilds ordered ops from recorded frontier snapshots.
func backtrack(a, b []lines.Line, trace []map[int]int, dd int) []Op {
	n, m := len(a), len(b)
	x, y := n, m
	rev := make([]Op, 0, n+m)
	for d := dd; d > 0; d-- {
		prev := trace[d-1]
		k := x - y
		var pk int
		if k == -d || (k != d && prev[k-1] < prev[k+1]) {
			pk = k + 1 // down edge (deletion boundary)
		} else {
			pk = k - 1 // right edge (insertion boundary)
		}
		px, py := prev[pk], prev[pk]-pk
		for x > px && y > py {
			rev = append(rev, Op{Kind: Equal, AI: x - 1, BI: y - 1})
			x--
			y--
		}
		if x == px {
			rev = append(rev, Op{Kind: Insert, AI: -1, BI: py})
		} else {
			rev = append(rev, Op{Kind: Delete, AI: px, BI: -1})
		}
		x, y = px, py
	}
	for x > 0 {
		rev = append(rev, Op{Kind: Equal, AI: x - 1, BI: y - 1})
		x--
		y--
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}

func bytesEqual(p, q []byte) bool {
	if len(p) != len(q) {
		return false
	}
	for i := range p {
		if p[i] != q[i] {
			return false
		}
	}
	return true
}
