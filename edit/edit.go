// Package edit 计算两个行序列之间的最短编辑脚本（Myers O(ND)）。
package edit

import (
	"bytes"
	"errors"
	"sync/atomic"
)

// ErrTooBig 表示编辑距离超过调用方给定的上限。
var ErrTooBig = errors.New("edit: 差异过大")

// Op 是一条编辑操作：' ' 保留、'-' 删除、'+' 插入。Line 含原行尾。
type Op struct {
	Kind byte
	Line []byte
}

var steps atomic.Int64 // 最近一次 Diff 在对角线上前进的总步数（含 snake）

// Steps 返回最近一次 Diff 的步数计数（用于复杂度审计）。
func Steps() int64 { return steps.Load() }

// Diff 返回把 a 变成 b 的最短编辑脚本。maxDist >= 0 时为编辑距离上限，
// 超过立即返回 ErrTooBig；maxDist < 0 表示不限。删除与插入并列时删除优先，
// 保证输出确定（见 DESIGN.md 第 3 节）。
func Diff(a, b [][]byte, maxDist int) ([]Op, error) {
	n, m := len(a), len(b)
	if n == 0 && m == 0 {
		return nil, nil
	}
	max := n + m
	if maxDist >= 0 && maxDist < max {
		max = maxDist
	}
	off := max
	v := make([]int, 2*max+1)
	var trace [][]int
	var count int64
	d, found := 0, false
	for ; d <= max && !found; d++ {
		trace = append(trace, append([]int(nil), v...))
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1] // 下移：插入
			} else {
				x = v[off+k-1] + 1 // 右移：删除（并列时优先）
			}
			y := x - k
			count++
			for x < n && y < m && bytes.Equal(a[x], b[y]) {
				x++
				y++
				count++
			}
			v[off+k] = x
			if x >= n && y >= m {
				found = true
				break
			}
		}
	}
	steps.Store(count)
	if !found {
		return nil, ErrTooBig
	}
	d--
	return backtrack(a, b, trace, d, off), nil
}

// backtrack 沿 trace 从 (n,m) 回溯到 (0,0)，逆序构造脚本后反转。
func backtrack(a, b [][]byte, trace [][]int, D, off int) []Op {
	var ops []Op
	x, y := len(a), len(b)
	for d := D; d >= 0; d-- {
		v := trace[d]
		k := x - y
		down := k == -d || (k != d && v[off+k-1] < v[off+k+1])
		prevK := k - 1
		if down {
			prevK = k + 1
		}
		prevX := v[off+prevK]
		prevY := prevX - prevK
		for x > prevX && y > prevY {
			x--
			y--
			ops = append(ops, Op{' ', a[x]})
		}
		if d == 0 {
			break
		}
		if down {
			ops = append(ops, Op{'+', b[prevY]})
		} else {
			ops = append(ops, Op{'-', a[prevX]})
		}
		x, y = prevX, prevY
	}
	for i, j := 0, len(ops)-1; i < j; i, j = i+1, j-1 {
		ops[i], ops[j] = ops[j], ops[i]
	}
	return ops
}

// Distance 返回脚本的编辑距离（删除数 + 插入数）。
func Distance(ops []Op) int {
	var d int
	for _, op := range ops {
		if op.Kind != ' ' {
			d++
		}
	}
	return d
}
