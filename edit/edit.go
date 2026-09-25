// Package edit 计算两个行序列之间的最短编辑脚本（Myers O(ND)）。
// 并列最短路径的取舍：V[k-1] == V[k+1] 时优先删除（向右走），
// 因此同一 hunk 内相邻的 '-'/'+' 行中 '-' 在前（见 DESIGN.md 第 3 节）。
package edit

import (
	"bytes"
	"errors"
)

// ErrTooLarge 表示编辑距离超过调用方给定的上限。
var ErrTooLarge = errors.New("edit: diff too large")

// Op 是编辑脚本的一步：' ' 保留、'-' 删除、'+' 插入；Text 为对应行（含行尾）。
type Op struct {
	Kind byte
	Text []byte
}

// Script 是有序的编辑操作序列。
type Script []Op

var steps int // 最近一次 Diff 在对角线上前进的总步数（含 snake 逐行比较）

// Steps 返回最近一次 Diff 的步数计数（用于复杂度审计）。
func Steps() int { return steps }

// Diff 返回把 a 变成 b 的最短编辑脚本。maxDist 为编辑距离上限，
// 传负数表示不限；超过上限立即返回 ErrTooLarge。
func Diff(a, b [][]byte, maxDist int) (Script, error) {
	n, m := len(a), len(b)
	steps = 0
	if n == 0 && m == 0 {
		return nil, nil
	}
	if maxDist < 0 || maxDist > n+m {
		maxDist = n + m
	}
	off := maxDist + 1 // V 数组下标偏移，k ∈ [-maxDist, maxDist]
	v := make([]int, 2*maxDist+3)
	var trace [][]int
	found := false
	var d int
	for d = 0; d <= maxDist && !found; d++ {
		for k := -d; k <= d; k += 2 {
			steps++
			var x int
			if k == -d || (k != d && v[k-1+off] < v[k+1+off]) {
				x = v[k+1+off] // 向下：插入
			} else {
				x = v[k-1+off] + 1 // 向右：删除（并列时优先）
			}
			y := x - k
			for x < n && y < m && bytes.Equal(a[x], b[y]) {
				x, y, steps = x+1, y+1, steps+1
			}
			v[k+off] = x
			if x >= n && y >= m {
				found = true
				break
			}
		}
		trace = append(trace, append([]int(nil), v...))
	}
	if !found {
		return nil, ErrTooLarge
	}
	return backtrack(a, b, trace, off), nil
}

// backtrack 沿 trace 回溯出脚本（前向顺序）。
func backtrack(a, b [][]byte, trace [][]int, off int) Script {
	var rev Script
	x, y := len(a), len(b)
	for d := len(trace) - 1; d > 0; d-- {
		vp := trace[d-1]
		k := x - y
		var pk int
		if k == -d || (k != d && vp[k-1+off] < vp[k+1+off]) {
			pk = k + 1 // 来自插入
		} else {
			pk = k - 1 // 来自删除
		}
		px, py := vp[pk+off], vp[pk+off]-pk
		for x > px && y > py {
			x, y = x-1, y-1
			rev = append(rev, Op{' ', a[x]})
		}
		if x == px {
			y--
			rev = append(rev, Op{'+', b[y]})
		} else {
			x--
			rev = append(rev, Op{'-', a[x]})
		}
	}
	for x > 0 && y > 0 {
		x, y = x-1, y-1
		rev = append(rev, Op{' ', a[x]})
	}
	out := make(Script, len(rev))
	for i, op := range rev {
		out[len(rev)-1-i] = op
	}
	return out
}
