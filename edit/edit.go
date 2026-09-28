// Package edit 用 Myers O(ND) 算法求两个行序列间的最短编辑脚本。
package edit

import (
	"errors"

	"ontology/lines"
)

// ErrTooDifferent 是编辑距离超过上限的可判定错误。
var ErrTooDifferent = errors.New("edit distance exceeds limit")

// Op 是一个原子操作：Equal/Delete 引用 a 行，Insert 引用 b 行。
type Op struct {
	Kind uint8 // 'e' 保留, 'd' 删除, 'i' 插入
	A    lines.Line
	B    lines.Line
}

// DiffSteps 是最近一次差分在对角线上前进的总步数（含 snake 逐行比较）。
var DiffSteps int

// Script 返回最短编辑脚本。并列时删除优先（见 DESIGN.md 第 3 节）。
// maxD < 0 表示不设限；实际编辑距离超过 maxD 时返回 ErrTooDifferent。
func Script(a, b []lines.Line, maxD int) ([]Op, error) {
	n, m := len(a), len(b)
	DiffSteps = 0
	if maxD < 0 {
		maxD = n + m
	}
	// trace[d] 是第 d 轮结束时各对角的 x；用 map 稀疏存储。
	trace := make([]map[int]int, 0, maxD+1)
	v := map[int]int{1: 0}
	var d int
	for d = 0; d <= maxD; d++ {
		snap := make(map[int]int, len(v))
		for k, x := range v {
			snap[k] = x
		}
		trace = append(trace, snap)
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[k-1] < v[k+1]) {
				x = v[k+1] // 向下（删除优先）：并列时走这条
			} else {
				x = v[k-1] + 1 // 向右插入
			}
			DiffSteps++ // 对角边步
			y := x - k
			for x < n && y < m {
				DiffSteps++ // snake 内逐行比较
				if a[x].Text != b[y].Text || a[x].End != b[y].End {
					break
				}
				x++
				y++
			}
			v[k] = x
			if x >= n && y >= m {
				return backtrack(a, b, trace, d), nil
			}
		}
	}
	return nil, ErrTooDifferent
}

func backtrack(a, b []lines.Line, trace []map[int]int, d int) []Op {
	x, y := len(a), len(b)
	var rev []Op
	for dd := d; dd > 0; dd-- {
		v := trace[dd]
		k := x - y
		var pk int
		if k == -dd || (k != dd && v[k-1] < v[k+1]) {
			pk = k + 1
		} else {
			pk = k - 1
		}
		px, py := v[pk], v[pk]-pk
		for x > px && y > py {
			rev = append(rev, Op{Kind: 'e', A: a[x-1], B: b[y-1]})
			x--
			y--
		}
		if x > px {
			rev = append(rev, Op{Kind: 'd', A: a[x-1]})
			x--
		} else {
			rev = append(rev, Op{Kind: 'i', B: b[y-1]})
			y--
		}
	}
	for x > 0 {
		rev = append(rev, Op{Kind: 'e', A: a[x-1], B: b[y-1]})
		x--
		y--
	}
	out := make([]Op, len(rev))
	for i, op := range rev {
		out[len(rev)-1-i] = op
	}
	return out
}

// Distance 返回脚本的删除数加插入数。
func Distance(ops []Op) int {
	t := 0
	for _, op := range ops {
		if op.Kind != 'e' {
			t++
		}
	}
	return t
}
