package edit

import "errors"

import "ontology/lines"

// Op 表示一对对应的删除/插入：行为空则表示另一侧是保留行。
type Op struct {
	Del lines.Line
	Ins lines.Line
}

// Script 为从 a 变到 b 的编辑脚本，Del/Ins 总行数即编辑距离。
type Script struct{ Ops []Op }

var ErrTooDifferent = errors.New("edit: difference exceeds limit")

var steps int64

// Steps 返回最近一次 Diff 在对角线上前进的总步数（含 snake 逐行比较）。
func Steps() int64 { return steps }

// Diff 用 Myers O(ND) 求最短编辑脚本。并列时横移（删除）优先，见 DESIGN.md。
// maxD>=0 时编辑距离超过它立即返回 ErrTooDifferent。
func Diff(a, b []lines.Line, maxD int) (Script, error) {
	steps = 0
	n, m := len(a), len(b)
	max := n + m
	if maxD >= 0 && maxD < max {
		max = maxD
	}
	v := make([]int, 2*(n+m)+3)
	off := n + m + 1
	type snap []int
	var trace []snap
	found := false
	for d := 0; d <= max; d++ {
		cp := make(snap, len(v))
		copy(cp, v)
		trace = append(trace, cp)
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[off+k-1] >= v[off+k+1]) {
				x = v[off+k-1] + 1 // 横移：删除优先
			} else {
				x = v[off+k+1] // 斜下移：插入
			}
			y := x - k
			for x < n && y < m {
				steps++
				if !lines.Equal(a[x], b[y]) {
					break
				}
				x++
				y++
			}
			v[off+k] = x
			if x >= n && y >= m {
				found = true
				break
			}
		}
		if found {
			return backtrack(a, b, trace), nil
		}
	}
	return Script{}, ErrTooDifferent
}

func backtrack(a, b []lines.Line, trace [][]int) Script {
	n, m := len(a), len(b)
	off := len(trace[0])/2 + 1
	x, y := n, m
	var ops []Op
	for d := len(trace) - 1; d >= 0; d-- {
		v := trace[d]
		k := x - y
		var pk int
		if k == -d || (k != d && v[off+k-1] >= v[off+k+1]) {
			pk = k - 1 // 来自横移
		} else {
			pk = k + 1
		}
		px := v[off+pk]
		py := px - pk
		for x > px && y > py {
			ops = append(ops, Op{Del: a[x-1], Ins: b[y-1]})
			x--
			y--
		}
		if d > 0 {
			if x == px+1 {
				ops = append(ops, Op{Del: a[x-1]})
			} else {
				ops = append(ops, Op{Ins: b[y-1]})
			}
		}
		x, y = px, py
	}
	for x > 0 && y > 0 {
		ops = append(ops, Op{Del: a[x-1], Ins: b[y-1]})
		x--
		y--
	}
	for i, j := 0, len(ops)-1; i < j; i, j = i+1, j-1 {
		ops[i], ops[j] = ops[j], ops[i]
	}
	return Script{Ops: ops}
}
