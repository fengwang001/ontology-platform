// Package edit 在两个行序列间求最短编辑脚本（Myers O(ND)）。
package edit

import (
	"errors"

	"ontology/lines"
)

// Op 为一个编辑操作。
type Op struct {
	Kind byte   // '=' 相同, '-' 删除, '+' 插入
	Line lines.Line
}

// 错误类别：差异超过调用方给出的编辑距离上限。
var ErrTooDifferent = errors.New("edit: edit distance exceeds maxD")

// Steps 是最近一次 Diff 在对角线上前进的总步数（含蛇行逐行比较）。
var Steps int

// Diff 返回 a→b 的最短编辑脚本。并列最短路径固定优先删除。
// maxD < 0 表示不设上限；超过上限时返回 ErrTooDifferent。
func Diff(a, b []lines.Line, maxD int) ([]Op, error) {
	Steps = 0
	n, m := len(a), len(b)
	type fp struct{ x int }
	v := map[int]int{1: 0}
	trace := []map[int]int{}
	limit := n + m
	if maxD >= 0 && maxD < limit {
		limit = maxD
	}
	for d := 0; d <= limit; d++ {
		snap := map[int]int{}
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[k-1] < v[k+1]) {
				x = v[k+1] // 向右：插入
			} else {
				x = v[k-1] + 1 // 向下：删除（并列时优先）
			}
			y := x - k
			for x < n && y < m {
				Steps++ // 蛇行：每比较一行计一步
				if a[x] != b[y] {
					break
				}
				x++
				y++
			}
			v[k] = x
			snap[k] = x
			if x >= n && y >= m {
				return backtrack(a, b, trace, n, m), nil
			}
		}
		trace = append(trace, snap)
	}
	return nil, ErrTooDifferent
}

// Distance 返回删除数+插入数。
func Distance(script []Op) int {
	d := 0
	for _, o := range script {
		if o.Kind != '=' {
			d++
		}
	}
	return d
}

func backtrack(a, b []lines.Line, trace []map[int]int, n, m int) []Op {
	ops := []Op{}
	x, y := n, m
	for d := len(trace) - 1; d >= 0; d-- {
		v := trace[d]
		k := x - y
		var pk int
		if k == -d || (k != d && v[k-1] < v[k+1]) {
			pk = k + 1
		} else {
			pk = k - 1
		}
		px, py := v[pk], v[pk]-pk
		for x > px && y > py {
			ops = append(ops, Op{Kind: '=', Line: a[x-1]})
			x--
			y--
		}
		if d > 0 || x != 0 || y != 0 {
			if x == px {
				ops = append(ops, Op{Kind: '+', Line: b[y-1]})
			} else {
				ops = append(ops, Op{Kind: '-', Line: a[x-1]})
			}
		}
		x, y = px, py
}
	for i, j := 0, len(ops)-1; i < j; i, j = i+1, j-1 {
		ops[i], ops[j] = ops[j], ops[i]
	}
	return ops
}
