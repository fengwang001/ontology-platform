package edit

import (
	"errors"

	"ontology/lines"
)

// ErrTooDifferent 表示编辑距离超过调用方给的上限。
var ErrTooDifferent = errors.New("edit: files differ beyond limit")

// Kind 是编辑脚本中的单步类型。
type Kind uint8

const (
	Equal Kind = iota
	Delete
	Insert
)

// Op 是一个编辑步（保持/删除/插入一行）。
type Op struct {
	Kind Kind
	Line lines.Line
}

// Stats 保存最近一次差分的内部计数。
type Stats struct{ steps int }

// Steps 返回蛇行逐行比较的总步数。
func (s *Stats) Steps() int { return s.steps }

// Diff 求最短编辑脚本；maxD<0 表示不设限。
func Diff(a, b []lines.Line, maxD int, st *Stats) ([]Op, error) {
	if st != nil {
		st.steps = 0
	}
	n, m := len(a), len(b)
	max := n + m
	if max == 0 {
		return nil, nil
	}
	off := max + 1
	v := make([]int, 2*max+3)
	var trace [][]int
	eq := func(x, y int) bool {
		if st != nil {
			st.steps++
		}
		return x < n && y < m && a[x] == b[y]
	}
	for d := 0; d <= max; d++ {
		snap := append([]int(nil), v...)
		trace = append(trace, snap)
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1]
			} else {
				x = v[off+k-1] + 1
			}
			y := x - k
			for eq(x, y) {
				x, y = x+1, y+1
			}
			v[off+k] = x
			if x >= n && y >= m {
				return backtrack(trace, a, b, off), nil
			}
		}
		if maxD >= 0 && d >= maxD {
			return nil, ErrTooDifferent
		}
	}
	return nil, ErrTooDifferent
}

func backtrack(trace [][]int, a, b []lines.Line, off int) []Op {
	n, m := len(a), len(b)
	x, y := n, m
	var rev []Op
	for d := len(trace) - 1; d > 0; d-- {
		prev := trace[d-1]
		k := x - y
		var pk, px int
		if k == -d || (k != d && prev[off+k-1] < prev[off+k+1]) {
			pk = k + 1
		} else {
			pk = k - 1
		}
		px = prev[off+pk]
		py := px - pk
		for x > px && y > py {
			rev = append(rev, Op{Kind: Equal, Line: a[x-1]})
			x, y = x-1, y-1
		}
		if x == px {
			rev = append(rev, Op{Kind: Insert, Line: b[y-1]})
			y--
		} else {
			rev = append(rev, Op{Kind: Delete, Line: a[x-1]})
			x--
		}
	}
	for x > 0 && y > 0 {
		rev = append(rev, Op{Kind: Equal, Line: a[x-1]})
		x, y = x-1, y-1
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}
