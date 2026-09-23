// Package edit 计算两个行序列之间的最短编辑脚本（Myers O(ND)）。
// 并列最短路径固定为"优先删除"：同一改动块内 - 行排在 + 行之前。
package edit

import "errors"

// ErrTooLarge 表示编辑距离超过调用方给定的上限。
var ErrTooLarge = errors.New("edit: distance exceeds limit")

// Op 是一条编辑操作：' ' 保留、'-' 删除 a[A]、'+' 插入 b[B]。
type Op struct {
	Kind byte
	A, B int
}

var lastSteps int64

// LastSteps 返回最近一次 Diff 在对角线上前进的总步数（含 snake 逐行比较）。
func LastSteps() int64 { return lastSteps }

// Diff 返回把 a 变成 b 的最短操作序列。max >= 0 时为编辑距离上限，
// 超过立即返回 ErrTooLarge；max < 0 表示不设上限。
func Diff(a, b []string, max int) ([]Op, error) {
	lastSteps = 0
	n, m := len(a), len(b)
	limit := n + m
	if max >= 0 && max < limit {
		limit = max
	}
	var trace [][]int
	v := []int{0}
	d := 0
	for ; ; d++ {
		if d > limit {
			return nil, ErrTooLarge
		}
		cur := make([]int, 2*d+1)
		done := false
		for k := -d; k <= d; k += 2 {
			lastSteps++
			var x int
			if k == -d || (k != d && v[k-1+d-1] < v[k+1+d-1]) {
				x = v[k+1+d-1] // 向下：插入
			} else {
				x = v[k-1+d-1] + 1 // 向右：删除（并列时优先）
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
				lastSteps++
			}
			cur[k+d] = x
			if x >= n && y >= m {
				done = true
			}
		}
		trace = append(trace, cur)
		if done {
			break
		}
		v = cur
	}
	return backtrack(a, b, trace, d), nil
}

func backtrack(a, b []string, trace [][]int, d int) []Op {
	var rev []Op
	x, y := len(a), len(b)
	for ; d > 0; d-- {
		v := trace[d-1]
		k := x - y
		var px, py int
		if k == -d || (k != d && v[k-1+d-1] < v[k+1+d-1]) {
			px, py = v[k+1+d-1], v[k+1+d-1]-(k+1)
			rev = append(rev, Op{'+', px, py})
		} else {
			px, py = v[k-1+d-1], v[k-1+d-1]-(k-1)
			rev = append(rev, Op{'-', px, py})
		}
		ex, ey := px, py
		if k == -d || (k != d && v[k-1+d-1] < v[k+1+d-1]) {
			ey++
		} else {
			ex++
		}
		for x > ex && y > ey {
			x--
			y--
			rev = append(rev, Op{' ', x, y})
		}
		x, y = px, py
	}
	for x > 0 && y > 0 {
		x--
		y--
		rev = append(rev, Op{' ', x, y})
	}
	ops := make([]Op, len(rev))
	for i, o := range rev {
		ops[len(rev)-1-i] = o
	}
	return ops
}
