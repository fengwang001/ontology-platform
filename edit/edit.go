// Package edit computes a shortest edit script between two line sequences
// using the Myers O(ND) algorithm. Ties always prefer deletes.
package edit

import (
	"errors"

	"ontology/lines"
)

// Op kind.
const (
	Equal byte = ' '
	Del   byte = '-'
	Ins   byte = '+'
)

// Step is one edit operation; Line is meaningful for all three kinds.
type Step struct {
	Op   byte
	Line lines.Line
}

// ErrTooDifferent is returned when the edit distance exceeds the configured cap.
var ErrTooDifferent = errors.New("edit: difference exceeds max edit distance")

// Counter records forward-diagonal progress (each per-line snake compare)
// made by the most recent Diff call.
type Counter struct{ Steps int }

// trace stores, per d round, the V map snapshot keyed by diagonal k.
type trace struct {
	vs []map[int]int
}

// Diff returns a shortest script transforming a into b. maxD < 0 disables
// the distance cap. Ties (same diagonal reachable from k-1 or k+1) prefer
// k+1, i.e. a delete.
func Diff(a, b []lines.Line, maxD int, c *Counter) ([]Step, error) {
	if c != nil {
		c.Steps = 0
	}
	n, m := len(a), len(b)
	v := map[int]int{1: 0}
	tr := trace{}
	max := n + m
	if maxD < 0 {
		maxD = max
	}
	for d := 0; d <= maxD; d++ {
		snap := make(map[int]int, len(v))
		for k, x := range v {
			snap[k] = x
		}
		tr.vs = append(tr.vs, snap)
		for k := -d; k <= d; k += 2 {
			var x int
			switch {
			case k == -d:
				x = v[k+1]
			case k == d:
				x = v[k-1] + 1
			default:
				x = down(k, v) // delete-first tie break
			}
			y := x - k
			for x < n && y < m && lines.Equal(a[x], b[y]) {
				if c != nil {
					c.Steps++
				}
				x, y = x+1, y+1
			}
			v[k] = x
			if x >= n && y >= m {
				s := backtrack(a, b, tr, d, c)
				return s, nil
			}
		}
	}
	return nil, ErrTooDifferent
}

func down(k int, v map[int]int) int {
	if v[k+1] > v[k-1]+1 {
		return v[k+1] // forced down (insert)
	}
	return v[k-1] + 1
}

func backtrack(a, b []lines.Line, tr trace, d int, c *Counter) []Step {
	var rev []Step
	x, y := len(a), len(b)
	for dd := d; dd > 0; dd-- {
		v := tr.vs[dd]
		k := x - y
		var pk int
		switch {
		case k == -dd:
			pk = k + 1
		case k == dd:
			pk = k - 1
		default:
			pk = k - 1
			if v[k+1] > v[k-1] {
				pk = k + 1
			}
		}
		px, py := v[pk], v[pk]-pk
		for x > px && y > py {
			rev = append(rev, Step{Equal, a[x-1]})
			x, y = x-1, y-1
		}
		if x == px {
			rev = append(rev, Step{Ins, b[y-1]})
			y--
		} else {
			rev = append(rev, Step{Del, a[x-1]})
			x--
		}
	}
	for x > 0 {
		rev = append(rev, Step{Equal, a[x-1]})
		x--
	}
	out := make([]Step, len(rev))
	for i, s := range rev {
		out[len(rev)-1-i] = s
	}
	return out
}

// Distance returns deletes plus inserts of a script.
func Distance(s []Step) int {
	n := 0
	for _, st := range s {
		if st.Op != Equal {
			n++
		}
	}
	return n
}
