// Package edit computes shortest edit scripts between two line sequences
// using Myers' O(ND) algorithm.
package edit

import (
	"errors"
	"sync/atomic"
)

// ErrTooBig is returned when the edit distance exceeds the configured limit.
var ErrTooBig = errors.New("edit: edit distance exceeds limit")

// Op is one edit-script operation.
// Kind is ' ' (keep), '-' (delete a[A]) or '+' (insert b[B]).
type Op struct {
	Kind byte
	A    int
	B    int
}

// steps counts diagonal advances (snake comparisons) made by the latest Diff.
var steps atomic.Int64

// Steps returns the counter value recorded by the most recent Diff call.
func Steps() int64 { return steps.Load() }

// Diff returns a shortest edit script transforming a into b. When several
// shortest scripts exist, deletion is preferred over insertion (see
// DESIGN.md section 3), which makes the result deterministic.
// If max >= 0 and the true distance exceeds max, ErrTooBig is returned.
func Diff(a, b []string, max int) ([]Op, error) {
	n, m := len(a), len(b)
	limit := n + m
	if max >= 0 && max < limit {
		limit = max
	}
	steps.Store(0)
	if n == 0 || m == 0 {
		if n+m > limit {
			return nil, ErrTooBig
		}
		ops := make([]Op, 0, n+m)
		for i := 0; i < n; i++ {
			ops = append(ops, Op{Kind: '-', A: i})
		}
		for j := 0; j < m; j++ {
			ops = append(ops, Op{Kind: '+', B: j})
		}
		return ops, nil
	}
	off := limit + 1
	v := make([]int, 2*limit+3)
	v[off+1] = 0
	var snap [][]int
	d := 0
	found := false
	for ; d <= limit; d++ {
		snap = append(snap, append([]int(nil), v[off-d-1:off+d+2]...))
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1] // down: insert
			} else {
				x = v[off+k-1] + 1 // right: delete (preferred on ties)
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x, y = x+1, y+1
				steps.Add(1)
			}
			v[off+k] = x
			if x >= n && y >= m {
				found = true
				break
			}
		}
		if found {
			break
		}
	}
	if !found {
		return nil, ErrTooBig
	}
	ops := make([]Op, 0, d+min(n, m))
	x, y := n, m
	for dd := d; dd >= 1; dd-- {
		prev := snap[dd]
		base := dd + 1
		k := x - y
		var pk int
		if k == -dd || (k != dd && prev[base+k-1] < prev[base+k+1]) {
			pk = k + 1
		} else {
			pk = k - 1
		}
		px, py := prev[base+pk], prev[base+pk]-pk
		for x > px && y > py {
			x, y = x-1, y-1
			ops = append(ops, Op{Kind: ' ', A: x, B: y})
		}
		if pk == k-1 {
			x--
			ops = append(ops, Op{Kind: '-', A: x})
		} else {
			y--
			ops = append(ops, Op{Kind: '+', B: y})
		}
	}
	for i, j := 0, len(ops)-1; i < j; i, j = i+1, j-1 {
		ops[i], ops[j] = ops[j], ops[i]
	}
	return ops, nil
}
