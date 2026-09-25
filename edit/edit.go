package edit

import (
	"errors"

	"ontology/lines"
)

// Kind identifies an edit-script operation.
type Kind int

const (
	Equal Kind = iota
	Delete
	Insert
)

// Op is one script entry; Line is the old line for Delete,
// the new line for Insert, and the common line for Equal.
type Op struct {
	Kind Kind
	Line lines.Line
}

// Script is the shortest edit sequence old -> new.
type Script []Op

// Distance is the number of deleted plus inserted ops.
func (s Script) Distance() int {
	n := 0
	for _, op := range s {
		if op.Kind != Equal {
			n++
		}
	}
	return n
}

// Options constrains a diff.
type Options struct {
	MaxDistance int // <= 0 means unlimited; exceeded -> ErrTooDifferent
}

var (
	// ErrTooDifferent is returned when edit distance exceeds MaxDistance.
	ErrTooDifferent = errors.New("edit: files differ by more than MaxDistance")
)

// steps counts diagonal forward steps (per-line snake comparisons)
// of the most recent Diff, including the failing final comparison.
var steps int

// Steps returns the non-exported counter value for tests.
func Steps() int { return steps }

// Diff computes the shortest edit script via Myers O(ND).
// Ties are resolved delete-first (see DESIGN.md section 3).
func Diff(a, b []lines.Line, opt Options) (Script, error) {
	steps = 0
	n, m := len(a), len(b)
	limit := n + m
	if opt.MaxDistance > 0 && opt.MaxDistance < limit {
		limit = opt.MaxDistance
	}
	max := n + m
	if max == 0 {
		return nil, nil
	}
	off := max + 1
	v := make([]int, 2*max+3)
	trace := make([][]int, 0, limit+1)
	var found []int
	done := 0
	for d := 0; d <= limit; d++ {
		done = d
		trace = append(trace, append([]int(nil), v...))
		for k := -d; k <= d; k += 2 {
			var x int
			switch {
			case k == -d:
				x = v[k+1+off]
			case k == d:
				x = v[k-1+off] + 1
			case v[k-1+off] < v[k+1+off]:
				x = v[k+1+off]
			default:
				x = v[k-1+off] + 1
			}
			y := x - k
			for x < n && y < m {
				steps++
				if !a[x].Equal(b[y]) {
					break
				}
				x++
				y++
			}
			v[k+off] = x
			if x >= n && y >= m {
				found = append([]int(nil), v...)
				goto back
			}
		}
	}
back:
	if found == nil {
		return nil, ErrTooDifferent
	}
	return backtrack(a, b, trace, found, done, off, n, m)
}

func backtrack(a, b []lines.Line, trace [][]int, last []int, d, off, n, m int) (Script, error) {
	x, y := n, m
	var rev Script
	for dd := d; dd > 0; dd-- {
		prev := trace[dd]
		k := x - y
		pk := k - 1
		if k == -dd || (k != dd && prev[k-1+off] < prev[k+1+off]) {
			pk = k + 1
		}
		px := prev[pk+off]
		py := px - pk
		for x > px && y > py {
			rev = append(rev, Op{Equal, a[x-1]})
			x--
			y--
		}
		switch {
		case x > px:
			rev = append(rev, Op{Delete, a[x-1]})
			x--
		default:
			rev = append(rev, Op{Insert, b[y-1]})
			y--
		}
	}
	for x > 0 {
		rev = append(rev, Op{Equal, a[x-1]})
		x--
	}
	out := make(Script, len(rev))
	for i, op := range rev {
		out[len(rev)-1-i] = op
	}
	return out, nil
}
