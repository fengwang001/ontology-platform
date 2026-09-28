// Package edit computes a shortest edit script between two line sequences
// using Myers' O(ND) algorithm. It depends only on package lines.
package edit

import (
	"errors"

	"ontology/lines"
)

// Op is one edit operation.
type Op struct {
	Kind byte // '=' context, '-' deleted from a, '+' inserted from b
	A    *lines.Line
	B    *lines.Line
}

// ErrTooLarge is returned when the edit distance exceeds the configured limit.
var ErrTooLarge = errors.New("edit: difference exceeds limit")

// steps counts diagonal advance steps (each line comparison, including the
// comparison that stops a snake) of the most recent Diff.
var steps int

// LastSteps returns the non-exported counter value of the most recent Diff.
func LastSteps() int { return steps }

func eq(a, b *lines.Line) bool {
	return string(a.Text) == string(b.Text) && string(a.EOL) == string(b.EOL)
}

// Diff returns a shortest script transforming a into b. A negative maxD means
// no limit; when the distance exceeds maxD it returns ErrTooLarge.
func Diff(a, b []lines.Line, maxD int) ([]Op, error) {
	steps = 0
	n, m := len(a), len(b)
	v := map[int]int{1: 0}
	trace := []map[int]int{}
	found := false
	for d := 0; ; d++ {
		for k := -d; k <= d; k += 2 {
			down := k == -d || (k != d && v[k-1] < v[k+1])
			if down {
				v[k] = v[k+1] // deletion edge: consume one b-side target? no: consume a
			} else {
				v[k] = v[k-1] + 1 // insertion edge
			}
			x, y := v[k], v[k]-k
			for x < n && y < m {
				steps++
				if !eq(&a[x], &b[y]) {
					break
				}
				x++
				y++
			}
			v[k] = x
			if x >= n && y >= m {
				found = true
			}
		}
		cp := make(map[int]int, len(v))
		for k, x := range v {
			cp[k] = x
		}
		trace = append(trace, cp)
		if found {
			break
		}
		if d >= maxD {
			return nil, ErrTooLarge
		}
	}
	// Backtrack from (n,m); ties prefer the deletion edge (down), per DESIGN.
	rev := []Op{}
	x, y := n, m
	for d := len(trace) - 1; d > 0; d-- {
		v, pv := trace[d], trace[d-1]
		// k of the diagonal (x,y) lies on after taking an edge and snake.
		k := x - y
		down := k == -d || (k != d && pv[k-1] < pv[k+1])
		end := pv[k+1]
		if !down {
			end = pv[k-1] + 1
		}
		for x > end {
			rev = append(rev, Op{Kind: '=', A: &a[x-1], B: &b[y-1]})
			x--
			y--
		}
		if down {
			rev = append(rev, Op{Kind: '-', A: &a[x-1]})
			x--
		} else {
			rev = append(rev, Op{Kind: '+', B: &b[y-1]})
			y--
		}
}
	for x > 0 {
		rev = append(rev, Op{Kind: '=', A: &a[x-1], B: &b[y-1]})
		x--
		y--
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev, nil
}

// Distance returns deletions plus insertions of the script.
func Distance(ops []Op) int {
	t := 0
	for _, o := range ops {
		if o.Kind != '=' {
			t++
		}
	}
	return t
}
