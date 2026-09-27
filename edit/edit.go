// Package edit 在两个行序列之间求最短编辑脚本（Myers O(ND)，删除优先）。
package edit

import (
	"errors"

	"ontology/lines"
)

// Op 是一条编辑操作。
type Op struct {
	Kind byte // ' ' 相等, '-' 删除, '+' 插入
	Old  lines.Line
	New  lines.Line
}

// ErrTooDifferent 表示编辑距离超过调用方给定上限。
var ErrTooDifferent = errors.New("edit: difference exceeds max distance")

// Script 持有编辑脚本及统计。
type Script struct {
	Ops []Op
	// Steps 为最近一次差分在对角线上前进的总步数（含 snake 逐行比较）。
	Steps int
}

// Dist 返回删除行数 + 插入行数。
func (s *Script) Dist() int {
	d := 0
	for _, o := range s.Ops {
		if o.Kind != ' ' {
			d++
		}
	}
	return d
}

// Diff 求 a→b 的最短脚本。maxD>=0 时距离超过它立刻返回 ErrTooDifferent。
func Diff(a, b []lines.Line, maxD int) (*Script, error) {
	n, m := len(a), len(b)
	steps := 0
	// trace[d] 是第 d 轮结束（含 snake）后的 k→x 快照，用于回溯。
	trace := []map[int]int{{1: 0}}
	v := map[int]int{1: 0}
	found := false
	limit := n + m
	if maxD >= 0 && maxD < limit {
		limit = maxD
	}
	for d := 0; d <= limit; d++ {
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[k-1] < v[k+1]) {
				x = v[k+1] // 向右（插入）
			} else {
				x = v[k-1] + 1 // 向下（删除）——并列时删除优先
			}
			y := x - k
			for x < n && y < m {
				steps++ // snake 逐行比较（含不匹配的那次）
				if string(a[x]) != string(b[y]) {
					break
				}
				x++
				y++
			}
			v[k] = x
			if x >= n && y >= m {
				found = true
				break
			}
		}
		snap := make(map[int]int, len(v))
		for k, x := range v {
			snap[k] = x
		}
		trace = append(trace, snap)
		if found {
			break
		}
	}
	if !found {
		return &Script{Steps: steps}, ErrTooDifferent
	}
	x, y := n, m
	var ops []Op
	for d := len(trace) - 1; d >= 1; d-- {
		prev := trace[d-1] // 本轮开始时的 V
		k := x - y
		var pk int
		if k == -d || (k != d && prev[k-1] < prev[k+1]) {
			pk = k + 1
		} else {
			pk = k - 1
		}
		px, py := prev[pk], prev[pk]-pk
		for x > px && y > py {
			ops = append(ops, Op{Kind: ' ', Old: a[x-1], New: b[y-1]})
			x--
			y--
		}
		if x == px {
			ops = append(ops, Op{Kind: '+', New: b[y-1]})
			y--
		} else {
			ops = append(ops, Op{Kind: '-', Old: a[x-1]})
			x--
		}
	}
	for i, j := 0, len(ops)-1; i < j; i, j = i+1, j-1 {
		ops[i], ops[j] = ops[j], ops[i]
	}
	return &Script{Ops: ops, Steps: steps}, nil
}
