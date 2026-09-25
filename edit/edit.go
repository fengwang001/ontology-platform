// Package edit 计算两个行序列之间的最短编辑脚本（Myers O(ND)）。
package edit

import (
	"errors"

	"ontology/lines"
)

// Kind 是编辑操作类型。
type Kind int

const (
	Equal Kind = iota
	Delete
	Insert
)

// Op 是一条编辑操作。
type Op struct {
	Kind Kind
	Line lines.Line
}

// Result 是差分结果。
type Result struct {
	Script []Op
	D      int
	Steps  int64
}

// ErrTooDifferent 表示编辑距离超过上限。
var ErrTooDifferent = errors.New("edit: difference exceeds max distance")

// Diff 计算最短脚本，maxD<0 表示不限制。Steps 为对角线前进总步数计数器。
func Diff(a, b []lines.Line, maxD int) (Result, error) {
	n, m := len(a), len(b)
	if maxD < 0 {
		maxD = n + m
	}
	var steps int64
	type snap map[int]int
	trace := []snap{}
	v := map[int]int{1: 0}
	var found bool
loop:
	for d := 0; d <= maxD; d++ {
		trace = append(trace, snap{})
		for k := -d; k <= d; k += 2 {
			var x int
			switch {
			case k == -d:
				x = v[k+1] // 删除优先的边界
			case k == d:
				x = v[k-1] + 1
			default:
				xd, xi := v[k+1], v[k-1]+1
				if xd >= xi { // 删除优先：并列时取 k+1
					x = xd
				} else {
					x = xi
				}
			}
			y := x - k
			for x < n && y < m && lines.Equal(a[x], b[y]) {
				steps++ // snake 逐行比较（命中）
				x++
				y++
			}
			if x < n || y < m {
				steps++ // snake 终止处的一次比较
			}
			v[k] = x
			trace[d][k] = x
			if x == n && y == m {
				found = true
				break loop
			}
		}
	}
	if !found {
		return Result{D: -1, Steps: steps}, ErrTooDifferent
	}
	x, y := n, m
	var ops []Op
	for d := len(trace) - 1; d > 0; d-- {
		k := x - y
		var pk int
		if k == -d || (k != d && trace[d-1][k+1] >= trace[d-1][k-1]+1) {
			pk = k + 1
		} else {
			pk = k - 1
		}
		px := trace[d-1][pk]
		py := px - pk
		for x > px && y > py {
			steps++
			ops = append(ops, Op{Kind: Equal, Line: a[x-1]})
			x--
			y--
		}
		steps++
		if x > px {
			ops = append(ops, Op{Kind: Delete, Line: a[x-1]})
			x--
		} else {
			ops = append(ops, Op{Kind: Insert, Line: b[y-1]})
			y--
		}
	}
	for x > 0 && y > 0 {
		ops = append(ops, Op{Kind: Equal, Line: a[x-1]})
		x--
		y--
	}
	for i, j := 0, len(ops)-1; i < j; i, j = i+1, j-1 {
		ops[i], ops[j] = ops[j], ops[i]
	}
	d := 0
	for _, o := range ops {
		if o.Kind != Equal {
			d++
		}
	}
	return Result{Script: ops, D: d, Steps: steps}, nil
}
