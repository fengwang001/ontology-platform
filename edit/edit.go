// Package edit 用 Myers O(ND) 算法求两个行序列之间的最短编辑脚本。
package edit

import (
	"errors"

	"ontology/lines"
)

// Op 是单个编辑操作。
type Op int

const (
	Equal Op = iota
	Delete
	Insert
)

// Item 是脚本中的一项；Old/New 仅在对应侧实际占行时有效。
type Item struct {
	Op  Op
	Old lines.Line
	New lines.Line
}

// ErrTooDifferent 表示编辑距离超过调用方给定的上限。
var ErrTooDifferent = errors.New("edit: distance exceeds limit")

// diagSteps 记录最近一次 Diff 在对角线上前进的总步数（含 snake 逐行比较）。
var diagSteps int64

// Diff 返回 a、b 之间最短（删除数+插入数最小）的编辑脚本。
// 并列最短脚本时删除优先（见 DESIGN.md 第 3 节）。
// maxDistance < 0 表示不设上限；超过上限立即返回 ErrTooDifferent。
func Diff(a, b []lines.Line, maxDistance int) ([]Item, error) {
	diagSteps = 0
	n, m := len(a), len(b)
	max := n + m
	if maxDistance >= 0 && maxDistance < max {
		max = maxDistance
	}
	v := map[int]int{1: 0}
	trace := make([]map[int]int, 0, max+1)
	var found bool
	for d := 0; d <= max && !found; d++ {
		for k := -d; k <= d; k += 2 {
			diagSteps++
			var x int
			if k == -d || (k != d && v[k-1] < v[k+1]) {
				x = v[k+1] // 向下：插入
			} else {
				x = v[k-1] + 1 // 向右：删除（k=0 并列时优先）
			}
			y := x - k
			for x < n && y < m && lines.Equal(a[x], b[y]) {
				diagSteps++
				x, y = x+1, y+1
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
	}
	if !found {
		return nil, ErrTooDifferent
	}
	return backtrack(a, b, trace), nil
}

func backtrack(a, b []lines.Line, trace []map[int]int) []Item {
	x := len(a)
	y := len(b)
	var items []Item
	for d := len(trace) - 1; d >= 0; d-- {
		v := trace[d]
		k := x - y
		var px, py int
		down := k == -d || (k != d && v[k-1] < v[k+1])
		if down {
			px, py = v[k+1], v[k+1]-(k+1)
		} else {
			px, py = v[k-1]+1, v[k-1]+1-(k-1)
		}
		for x > px && y > py {
			items = append(items, Item{Op: Equal, Old: a[x-1], New: b[y-1]})
			x, y = x-1, y-1
		}
		if d > 0 {
			if down {
				items = append(items, Item{Op: Insert, New: b[y-1]})
			} else {
				items = append(items, Item{Op: Delete, Old: a[x-1]})
			}
			x, y = px, py
		}
	}
	for x > 0 && y > 0 {
		items = append(items, Item{Op: Equal, Old: a[x-1], New: b[y-1]})
		x, y = x-1, y-1
	}
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
	return items
}
