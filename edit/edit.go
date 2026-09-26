// Package edit computes a shortest edit script between two line sequences
// with the Myers O(ND) algorithm. Ties are broken in favour of deletions.
package edit

import (
	"bytes"
	"errors"
	"fmt"

	"ontology/lines"
)

// Op identifies one kind of edit-script step.
type Op int

const (
	Equal  Op = iota // line present in both
	Delete           // line only in the old text
	Insert           // line only in the new text
)

// Step is one script operation. Old is set for Equal/Delete, New for Equal/Insert.
type Step struct {
	Op  Op
	Old *lines.Line
	New *lines.Line
}

// ErrTooDifferent is returned when the edit distance exceeds the configured cap.
var ErrTooDifferent = errors.New("edit: edit distance exceeds configured limit")

// Options controls Diff. MaxDistance 0 means len(a)+len(b).
type Options struct {
	MaxDistance int
}

var compareCount int64

// Steps returns the number of diagonal line comparisons made by the latest Diff.
func Steps() int64 { return compareCount }

// Diff returns a minimum-length script turning a into b.
func Diff(a, b []lines.Line, opts Options) ([]Step, error) {
	compareCount = 0
	n, m := len(a), len(b)
	maxD := opts.MaxDistance
	if maxD <= 0 || maxD > n+m {
		maxD = n + m
	}
	off := maxD + 1
	v := make([]int, 2*off+1) // indexed by k+off
	trace := make([][]int, 0, maxD+1)
	foundX, foundY := 0, 0
	for d := 0; d <= maxD; d++ {
		for k := -d; k <= d; k += 2 {
			var x int
			switch {
			case k == -d:
				x = v[k+1+off] // right (insert)
			case k == d:
				x = v[k-1+off] + 1 // down (delete)
			default:
				if v[k-1+off] < v[k+1+off] {
					x = v[k+1+off]
				} else {
					x = v[k-1+off] + 1 // ties: prefer delete
				}
			}
			y := x - k
			for x < n && y < m {
				compareCount++
				if !bytes.Equal(a[x].Data, b[y].Data) {
					break
				}
				x++
				y++
			}
			v[k+off] = x
		}
		snap := make([]int, len(v))
		copy(snap, v)
		trace = append(trace, snap)
		end := v[n-m+off] // only diagonal k=n-m can reach (n,m)
		if end >= n {
			foundX, foundY = n, m
			break
		}
		if d == maxD {
			return nil, fmt.Errorf("%w (max=%d)", ErrTooDifferent, maxD)
		}
	}
	return backtrack(a, b, trace, foundX, foundY), nil
}

func backtrack(a, b []lines.Line, trace [][]int, ex, ey int) []Step {
	x, y := ex, ey
	var rev []Step
	for d := len(trace) - 1; d >= 0; d-- {
		if d == 0 {
			for x > 0 && y > 0 {
				rev = append(rev, Step{Op: Equal, Old: &a[x-1], New: &b[y-1]})
				x--
				y--
			}
			break
		}
		prev := trace[d-1]
		off := len(prev) / 2
		k := x - y
		var pk int
		if k == -d || (k != d && prev[k-1+off] < prev[k+1+off]) {
			pk = k + 1
		} else {
			pk = k - 1
		}
		px, py := prev[pk+off], prev[pk+off]-pk
		for x > px && y > py {
			rev = append(rev, Step{Op: Equal, Old: &a[x-1], New: &b[y-1]})
			x--
			y--
		}
		if x == px+1 && y == py {
			rev = append(rev, Step{Op: Delete, Old: &a[x-1]})
		} else {
			rev = append(rev, Step{Op: Insert, New: &b[y-1]})
		}
		x, y = px, py
	}
	for x > 0 && y > 0 {
		rev = append(rev, Step{Op: Equal, Old: &a[x-1], New: &b[y-1]})
		x--
		y--
	}
	out := make([]Step, len(rev))
	for i, s := range rev {
		out[len(rev)-1-i] = s
	}
	return out
}
