// Package edit 计算两个行序列之间的最短编辑脚本（Myers O(ND)）。
package edit

import (
	"errors"
	"sync/atomic"
)

// ErrTooLarge 表示最小编辑距离超过调用方给定的上限。
var ErrTooLarge = errors.New("edit: 差异过大")

// Kind 是编辑操作类型。
type Kind byte

const (
	Keep Kind = iota // 行在两侧相同
	Del              // 从旧序列删除
	Ins              // 插入新序列的行
)

// Op 是一条编辑操作，Text 为涉及行的完整内容（含行尾）。
type Op struct {
	Kind Kind
	Text string
}

var lastSteps atomic.Int64

// LastSteps 返回最近一次 Diff 在对角线上前进的总步数（含 snake 逐行比较）。
func LastSteps() int64 { return lastSteps.Load() }

// Diff 返回把 a 变成 b 的最短编辑脚本。limit >= 0 时，若最小编辑距离
// 超过 limit 则立即返回 ErrTooLarge；limit < 0 表示不设上限。
// 平局时优先删除（见 DESIGN.md 第 3 节），输出确定。
func Diff(a, b []string, limit int) ([]Op, error) {
	n, m := len(a), len(b)
	max := n + m
	if max == 0 {
		return nil, nil
	}
	off := max
	v := make([]int, 2*max+1)
	var trace [][]int
	var steps int64
	found := -1
loop:
	for d := 0; d <= max; d++ {
		if limit >= 0 && d > limit {
			break
		}
		cp := make([]int, len(v))
		copy(cp, v)
		trace = append(trace, cp)
		for k := -d; k <= d; k += 2 {
			steps++
			var x int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1]
			} else {
				x = v[off+k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
				steps++
			}
			v[off+k] = x
			if x >= n && y >= m {
				found = d
				break loop
			}
		}
	}
	lastSteps.Store(steps)
	if found < 0 {
		return nil, ErrTooLarge
	}
	return backtrack(a, b, trace, found), nil
}

// backtrack 沿 trace 从 (n,m) 回放到 (0,0)，生成正向脚本。
func backtrack(a, b []string, trace [][]int, d int) []Op {
	x, y := len(a), len(b)
	off := len(a) + len(b)
	var rev []Op
	for ; d > 0; d-- {
		v := trace[d]
		k := x - y
		var px, py int
		if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
			px = v[off+k+1]
			py = px - (k + 1)
		} else {
			px = v[off+k-1]
			py = px - (k - 1)
		}
		for x > px && y > py {
			rev = append(rev, Op{Keep, a[x-1]})
			x--
			y--
		}
		if x == px {
			rev = append(rev, Op{Ins, b[y-1]})
			y--
		} else {
			rev = append(rev, Op{Del, a[x-1]})
			x--
		}
	}
	for x > 0 && y > 0 {
		rev = append(rev, Op{Keep, a[x-1]})
		x--
		y--
	}
	out := make([]Op, len(rev))
	for i, o := range rev {
		out[len(rev)-1-i] = o
	}
	return out
}
