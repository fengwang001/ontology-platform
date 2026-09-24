package edit

import (
	"errors"

	"ontology/lines"
)

var ErrTooDifferent = errors.New("edit distance exceeds limit")

type Op struct {
	Kind byte
	Line lines.Line
}

type Script struct {
	Ops      []Op
	Compare  int
	Distance int
}

var counter int

func Diff(a, b []lines.Line, maxDistance int) (Script, error) {
	counter = 0
	n, m := len(a), len(b)
	maxK := n + m
	offset := maxK
	v := make([]int, 2*maxK+1)
	var trace [][]int
	for d := 0; d <= maxK; d++ {
		if maxDistance > 0 && d > maxDistance {
			return Script{Compare: counter, Distance: d}, ErrTooDifferent
		}
		for k := -d; k <= d; k += 2 {
			counter++
			var x int
			if k == -d || (k != d && v[offset+k-1] < v[offset+k+1]) {
				x = v[offset+k+1]
			} else {
				x = v[offset+k-1] + 1
			}
			y := x - k
			for x < n && y < m {
				counter++
				if a[x] != b[y] {
					break
				}
				x, y = x+1, y+1
			}
			v[offset+k] = x
			if x == n && y == m {
				snapshot := make([]int, len(v))
				copy(snapshot, v)
				trace = append(trace, snapshot)
				ops, err := backtrack(a, b, trace)
				if err != nil {
					return Script{Compare: counter}, err
				}
				return Script{Ops: ops, Compare: counter, Distance: d}, nil
			}
		}
		snapshot := make([]int, len(v))
		copy(snapshot, v)
		trace = append(trace, snapshot)
	}
	return Script{Compare: counter}, ErrTooDifferent
}

func backtrack(a, b []lines.Line, trace [][]int) ([]Op, error) {
	x, y := len(a), len(b)
	offset := len(a) + len(b)
	var ops []Op
	for d := len(trace) - 1; d >= 0; d-- {
		v := trace[d]
		k := x - y
		previousK := k - 1
		if k == -d || (k != d && v[offset+k-1] < v[offset+k+1]) {
			previousK = k + 1
		}
		previousX := v[offset+previousK]
		previousY := previousX - previousK
		for x > previousX && y > previousY {
			x, y = x-1, y-1
			ops = append(ops, Op{Kind: ' ', Line: a[x]})
		}
		if d > 0 {
			if x == previousX {
				ops = append(ops, Op{Kind: '+', Line: b[previousY]})
			} else {
				ops = append(ops, Op{Kind: '-', Line: a[previousX]})
			}
		}
		x, y = previousX, previousY
	}
	reverse(ops)
	return ops, nil
}

func reverse(ops []Op) {
	for left, right := 0, len(ops)-1; left < right; left, right = left+1, right-1 {
		ops[left], ops[right] = ops[right], ops[left]
	}
}
