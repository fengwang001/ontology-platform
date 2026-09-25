// Package edit computes a shortest edit script between two line sequences
// using Myers' O(ND) algorithm. Ties are broken in favor of deletion.
package edit

import (
	"errors"

	"ontology/lines"
)

// ErrTooLarge is returned when the edit distance exceeds the configured cap.
var ErrTooLarge = errors.New("edit: difference too large")

// Op is one step of an edit script. Kind is ' ' (keep), '-' (delete a[Old])
// or '+' (insert b[New]). Unused indices are -1.
type Op struct {
	Kind     byte
	Old, New int
}

var lastSteps int64

// Steps returns the number of diagonal-advance steps (k-loop visits plus
// per-line snake comparisons) performed by the most recent Diff call.
func Steps() int64 { return lastSteps }

// Diff returns a shortest script turning a into b. maxDist >= 0 caps the
// edit distance; exceeding it yields ErrTooLarge.
func Diff(a, b [][]byte, maxDist int) ([]Op, error) {
	lastSteps = 0
	pre := 0
	for pre < len(a) && pre < len(b) && lines.Equal(a[pre], b[pre]) {
		pre++
	}
	a, b = a[pre:], b[pre:]
	suf := 0
	for suf < len(a) && suf < len(b) && lines.Equal(a[len(a)-1-suf], b[len(b)-1-suf]) {
		suf++
	}
	a, b = a[:len(a)-suf], b[:len(b)-suf]
	n, m := len(a), len(b)
	var mid []Op
	if n > 0 && m > 0 {
		limit := n + m
		if maxDist >= 0 && maxDist < limit {
			limit = maxDist
		}
		var err error
		mid, err = myers(a, b, pre, limit)
		if err != nil {
			return nil, err
		}
	} else {
		for i := range a {
			mid = append(mid, Op{'-', pre + i, -1})
		}
		for j := range b {
			mid = append(mid, Op{'+', -1, pre + j})
		}
	}
	ops := make([]Op, 0, pre+len(mid)+suf)
	for i := 0; i < pre; i++ {
		ops = append(ops, Op{' ', i, i})
	}
	ops = append(ops, mid...)
	for i := 0; i < suf; i++ {
		ops = append(ops, Op{' ', pre + n + i, pre + m + i})
	}
	return ops, nil
}

func myers(a, b [][]byte, base, limit int) ([]Op, error) {
	n, m := len(a), len(b)
	off := limit + 1
	v := make([]int, 2*off+1)
	var trace [][]int
	found := -1
	for d := 0; d <= limit && found < 0; d++ {
		for k := -d; k <= d; k += 2 {
			lastSteps++
			x := v[k+1+off]
			if k == -d || (k != d && v[k-1+off] < v[k+1+off]) {
				x = v[k+1+off] // down: insertion
			} else {
				x = v[k-1+off] + 1 // right: deletion (wins ties)
			}
			y := x - k
			for x < n && y < m && lines.Equal(a[x], b[y]) {
				x, y = x+1, y+1
				lastSteps++
			}
			v[k+off] = x
			if x >= n && y >= m {
				found = d
			}
		}
		snap := make([]int, 2*d+1)
		copy(snap, v[-d+off:d+off+1])
		trace = append(trace, snap)
	}
	if found < 0 {
		return nil, ErrTooLarge
	}
	x, y := n, m
	rev := make([]Op, 0, found)
	for d := found; d > 0; d-- {
		prev := trace[d-1]
		k := x - y
		pk := k + 1
		if k == -d || (k != d && prev[k-1+d-1] < prev[k+1+d-1]) {
			pk = k + 1 // came from insertion
		} else {
			pk = k - 1 // came from deletion
		}
		px, py := prev[pk+d-1], prev[pk+d-1]-pk
		if pk == k+1 {
			for x > px && y > py+1 {
				x, y = x-1, y-1
				rev = append(rev, Op{' ', base + x, base + y})
			}
			rev = append(rev, Op{'+', -1, base + py})
		} else {
			for x > px+1 && y > py {
				x, y = x-1, y-1
				rev = append(rev, Op{' ', base + x, base + y})
			}
			rev = append(rev, Op{'-', base + px, -1})
		}
		x, y = px, py
	}
	for x > 0 {
		x, y = x-1, y-1
		rev = append(rev, Op{' ', base + x, base + y})
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev, nil
}
