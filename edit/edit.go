// Package edit 用 Myers O(ND) 算法求两个行序列之间的最短编辑脚本。
package edit

import (
	"errors"

	"ontology/lines"
)

// ErrTooDifferent 表示编辑距离超过调用方给定的上限。
var ErrTooDifferent = errors.New("edit: distance exceeds max")

// Kind 是编辑操作种类。
type Kind int

const (
	Equal Kind = iota
	Delete
	Insert
)

// Op 是一条编辑操作：Equal 时 A==B；Delete 只取 A；Insert 只取 B。
type Op struct {
	Kind Kind
	A, B lines.Line
}

// Script 是最短编辑脚本。Distance 为删除数+插入数；Steps 为最近一次
// 差分在对角线上前进的总步数（含 snake 逐行比较），用于复杂度自检。
type Script struct {
	Ops      []Op
	Distance int
	Steps    int64
}

// Diff 返回 a→b 的最短编辑脚本。maxD>0 时为编辑距离上限，超限返回
// ErrTooDifferent；maxD<=0 表示不限。并列最短路径固定优先删除。
func Diff(a, b []lines.Line, maxD int) (*Script, error) {
	n, m := len(a), len(b)
	limit := n + m
	if maxD > 0 && maxD < limit {
		limit = maxD
	}
	off := limit + 1
	v := make([]int, 2*off+1)
	v[off] = 0
	trace := make([][]int, 0, limit+1)
	var steps int64
	found := false
	var fx, fy int
	for d := 0; d <= limit && !found; d++ {
		trace = append(trace, append([]int(nil), v...))
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[k-1+off] < v[k+1+off]) {
				x = v[k+1+off] // 向下：删除
			} else {
				x = v[k-1+off] + 1 // 向右：插入
			}
			y := x - k
			steps++ // 计入一条编辑边
			for x < n && y < m {
				steps++ // 计入一次 snake 逐行比较
				if a[x] != b[y] {
					break
				}
				x, y = x+1, y+1
			}
			v[k+off] = x
			if x >= n && y >= m {
				fx, fy, found = x, y, true
				break
			}
		}
	}
	if !found {
		return &Script{Steps: steps}, ErrTooDifferent
	}
	ops := backtrack(a, b, trace, fx, fy, off)
	dist := 0
	for _, op := range ops {
		if op.Kind != Equal {
			dist++
		}
	}
	return &Script{Ops: ops, Distance: dist, Steps: steps}, nil
}

func backtrack(a, b []lines.Line, trace [][]int, ex, ey, off int) []Op {
	x, y := ex, ey
	var ops []Op
	for d := len(trace) - 1; d > 0; d-- {
		v := trace[d-1]
		k := x - y
		var pk int
		if k == -d || (k != d && v[k-1+off] < v[k+1+off]) {
			pk = k + 1
		} else {
			pk = k - 1
		}
		px, py := v[pk+off], v[pk+off]-pk
		for x > px && y > py {
			ops = append(ops, Op{Kind: Equal, A: a[x-1], B: b[y-1]})
			x, y = x-1, y-1
		}
		if d > 0 {
			if x > px {
				ops = append(ops, Op{Kind: Delete, A: a[x-1]})
				x--
			} else {
				ops = append(ops, Op{Kind: Insert, B: b[y-1]})
				y--
			}
		}
	}
	for x > 0 && y > 0 {
		ops = append(ops, Op{Kind: Equal, A: a[x-1], B: b[y-1]})
		x, y = x-1, y-1
	}
	for i := 0; i < len(ops)/2; i++ {
		j := len(ops) - 1 - i
		ops[i], ops[j] = ops[j], ops[i]
	}
	return ops
}
