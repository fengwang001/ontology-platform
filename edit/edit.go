// Package edit 计算两个行序列之间的最短编辑脚本（Myers O(ND)），
// 带编辑距离上限。依赖 lines。
package edit

import (
	"errors"

	"ontology/lines"
)

// 操作种类。
const (
	Equal = ' '
	Del   = '-'
	Ins   = '+'
)

// Op 是一个编辑操作。A、B 是操作发生时在旧/新行序列中的位置：
// Equal/Del 消费 A 行，Ins 消费 B 行；未消费一侧的字段记录当前已消费行数。
type Op struct {
	Kind byte
	A, B int
}

// Script 是最短编辑脚本及其对应的两侧行序列。
type Script struct {
	Ops  []Op
	A, B []string
}

// ErrTooLarge 表示编辑距离超过上限。
var ErrTooLarge = errors.New("edit: difference too large")

// lastSteps 是最近一次 Diff 在对角线上前进的总步数（含 snake 逐行比较）。
var lastSteps int

// LastSteps 返回最近一次 Diff 的步数计数（用于复杂度审计）。
func LastSteps() int { return lastSteps }

// Diff 计算 a 到 b 的最短编辑脚本。maxDist >= 0 时为编辑距离上限，
// 超过则返回 ErrTooLarge；maxDist < 0 表示不限。并列时固定优先删除。
func Diff(a, b []byte, maxDist int) (*Script, error) {
	al, bl := lines.Split(a), lines.Split(b)
	n, m := len(al), len(bl)
	max := n + m
	if maxDist >= 0 && maxDist < max {
		max = maxDist
	}
	off := max + 1
	v := make([]int, 2*max+3)
	var trace [][]int
	steps, D, found := 0, 0, false
	for d := 0; d <= max && !found; d++ {
		for k := -d; k <= d; k += 2 {
			steps++
			var x int
			if k == -d || (k != d && v[k-1+off] < v[k+1+off]) {
				x = v[k+1+off] // 向下：插入
			} else {
				x = v[k-1+off] + 1 // 向右：删除（并列时走这里）
			}
			y := x - k
			for x < n && y < m && al[x] == bl[y] {
				x, y, steps = x+1, y+1, steps+1
			}
			v[k+off] = x
			if x >= n && y >= m {
				D, found = d, true
				break
			}
		}
		trace = append(trace, append([]int(nil), v[off-d:off+d+1]...))
	}
	lastSteps = steps
	if !found {
		return nil, ErrTooLarge
	}
	return &Script{Ops: backtrack(trace, D, n, m), A: al, B: bl}, nil
}

// backtrack 从 trace 还原脚本；trace[d] 是第 d 轮后的 V，下标 k 对应 [k+d]。
func backtrack(trace [][]int, D, n, m int) []Op {
	var ops []Op
	x, y := n, m
	for d := D; d > 0; d-- {
		prev := trace[d-1]
		k := x - y
		var pk int
		if k == -d || (k != d && prev[k-1+d-1] < prev[k+1+d-1]) {
			pk = k + 1 // 上一步是插入
		} else {
			pk = k - 1 // 上一步是删除（并列时走这里）
		}
		px, py := prev[pk+d-1], prev[pk+d-1]-pk
		for x > px && y > py {
			ops = append(ops, Op{Equal, x - 1, y - 1})
			x, y = x-1, y-1
		}
		if x == px {
			ops = append(ops, Op{Ins, x, y - 1})
			y--
		} else {
			ops = append(ops, Op{Del, x - 1, y})
			x--
		}
	}
	for x > 0 && y > 0 {
		ops = append(ops, Op{Equal, x - 1, y - 1})
		x, y = x-1, y-1
	}
	for i, j := 0, len(ops)-1; i < j; i, j = i+1, j-1 {
		ops[i], ops[j] = ops[j], ops[i]
	}
	return ops
}
