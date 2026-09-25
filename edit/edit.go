// Package edit 计算两个行序列之间的最小编辑脚本（Myers O(ND)）。
package edit

import (
	"bytes"
	"errors"
	"sync/atomic"
)

// ErrTooLarge 表示编辑距离超过调用方给定的上限。
var ErrTooLarge = errors.New("edit: difference too large")

// Op 是编辑脚本的一步。
type Op byte

const (
	Keep Op = iota // 该行在两侧相同
	Del            // 删除 a 的一行
	Ins            // 插入 b 的一行
)

// Script 是最短编辑脚本，Del 数 + Ins 数等于最小编辑距离。
type Script []Op

var lastSteps atomic.Int64 // 最近一次 Diff 在对角线上前进的总步数（非导出计数器）

// Steps 返回最近一次 Diff 的步数计数（含 snake 的逐行比较）。
func Steps() int64 { return lastSteps.Load() }

// Diff 返回把 a 变成 b 的最短编辑脚本。maxDist >= 0 时为编辑距离上限，
// 超过立即返回 ErrTooLarge。并列最短路径固定优先删除（见 DESIGN.md 第 3 节）。
func Diff(a, b [][]byte, maxDist int) (Script, error) {
	n, m := len(a), len(b)
	var steps int64
	defer func() { lastSteps.Store(steps) }()
	if n == 0 && m == 0 {
		return nil, nil
	}
	max := n + m
	v := make([]int, 2*max+1)
	off := max
	var trace [][]int // trace[d] 保存第 d 层各 k 的 x（k=-d,-d+2,...,d）
	found := -1
	for d := 0; d <= max; d++ {
		if maxDist >= 0 && d > maxDist {
			return nil, ErrTooLarge
		}
		snap := make([]int, d+1)
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[k-1+off] < v[k+1+off]) {
				x = v[k+1+off] // 向下：插入
			} else {
				x = v[k-1+off] + 1 // 向右：删除（并列时走这里）
			}
			y := x - k
			steps++
			for x < n && y < m && bytes.Equal(a[x], b[y]) {
				x, y, steps = x+1, y+1, steps+1
			}
			v[k+off] = x
			snap[(k+d)/2] = x
			if x >= n && y >= m {
				found = d
				break
			}
		}
		trace = append(trace, snap)
		if found >= 0 {
			break
		}
	}
	return backtrack(trace, found, n, m), nil
}

func at(trace [][]int, d, k int) int { return trace[d][(k+d)/2] }

func backtrack(trace [][]int, depth, n, m int) Script {
	var rev []Op
	x, y := n, m
	for d := depth; d > 0; d-- {
		k := x - y
		var pk int
		if k == -d || (k != d && at(trace, d-1, k-1) < at(trace, d-1, k+1)) {
			pk = k + 1 // 上一步是插入
		} else {
			pk = k - 1 // 上一步是删除
		}
		px, py := at(trace, d-1, pk), at(trace, d-1, pk)-pk
		for x > px && y > py {
			rev = append(rev, Keep)
			x, y = x-1, y-1
		}
		if x == px {
			rev = append(rev, Ins)
			y--
		} else {
			rev = append(rev, Del)
			x--
		}
	}
	for x > 0 && y > 0 { // 第 0 层的 snake
		rev = append(rev, Keep)
		x, y = x-1, y-1
	}
	s := make(Script, len(rev))
	for i, op := range rev {
		s[len(rev)-1-i] = op
	}
	return s
}
