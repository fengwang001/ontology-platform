// Package edit 在两个行序列之间求最短编辑脚本（Myers O(ND)）。
package edit

import (
	"errors"

	"ontology/lines"
)

// OpKind 是编辑脚本中的操作类型。
type OpKind uint8

const (
	Equal  OpKind = iota // 公共行
	Delete               // 仅存在于旧文件
	Insert               // 仅存在于新文件
)

// Op 是脚本中的一条操作：Equal 时 Old 与 New 为同一公共行；
// Delete 只填 Old；Insert 只填 New。
type Op struct {
	Kind OpKind
	Old  lines.Line
	New  lines.Line
}

// ErrTooDifferent 表示编辑距离超过调用方给出的上限。
var ErrTooDifferent = errors.New("edit: edit distance exceeds limit")

// steps 记录最近一次 Diff 在对角线上前进的总步数（含 snake 的逐行比较）。
var steps int64

// Steps 返回非导出计数器 steps（测试用于钉复杂度上界）。
func Steps() int64 { return steps }

type point struct{ x, y int }

// Diff 返回 a→b 的最短编辑脚本。maxD>=0 时编辑距离超过它立刻返回 ErrTooDifferent。
// 并列最短路径固定选择删除优先（见 DESIGN.md 第 3 节）。
func Diff(a, b []lines.Line, maxD int) ([]Op, error) {
	steps = 0
	n, m := len(a), len(b)
	if maxD < 0 || maxD > n+m {
		maxD = n + m
	}
	off := maxD + 1
	v := make([]int, 2*off+1)
	trace := make([][]int, 0, maxD+1)
	for d := 0; d <= maxD; d++ {
		snap := make([]int, len(v))
		copy(snap, v)
		trace = append(trace, snap)
		for k := -d; k <= d; k += 2 {
			steps++ // 一次对角线下/右前进步
			var x int
			switch {
			case k == -d || (k != d && v[off+k-1] < v[off+k+1]):
				x = v[off+k+1] // 右行（插入）
			default:
				x = v[off+k-1] + 1 // 下行（删除），并列时取此分支
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				steps++ // snake 内逐行比较并前进一步
				x, y = x+1, y+1
			}
			v[off+k] = x
			if x >= n && y >= m {
				return backtrack(a, b, trace, off), nil
			}
		}
	}
	return nil, ErrTooDifferent
}

func backtrack(a, b []lines.Line, trace [][]int, off int) []Op {
	var ops []Op
	x, y := len(a), len(b)
	for d := len(trace) - 1; d >= 0; d-- {
		v := trace[d]
		k := x - y
		var pk int
		if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
			pk = k + 1
		} else {
			pk = k - 1
		}
		px := v[off+pk]
		py := px - pk
		for x > px && y > py {
			ops = append(ops, Op{Kind: Equal, Old: a[x-1], New: b[y-1]})
			x, y = x-1, y-1
		}
		if d > 0 {
			if x == px {
				ops = append(ops, Op{Kind: Insert, New: b[y-1]})
				y--
			} else {
				ops = append(ops, Op{Kind: Delete, Old: a[x-1]})
				x--
			}
		}
	}
	for i := len(ops) - 1; i >= 0; i-- { // 反转成正序
		if i < len(ops)/2 {
			ops[i], ops[len(ops)-1-i] = ops[len(ops)-1-i], ops[i]
		}
	}
	return ops
}
