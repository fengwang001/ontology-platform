// Package edit 计算两个行序列之间的最短编辑脚本（Myers O(ND)）。
package edit

import "ontology/lines"

// Op 是一条原子编辑：Del 删除 a[Ai]，Ins 插入 b[Bi]。
type Op struct {
	Kind int // 1=Del, 2=Ins
	Ai   int
	Bi   int
	Line lines.Line
}

const (
	Del = 1
	Ins = 2
)

// ErrTooLarge 是编辑距离超过上限时返回的哨兵错误。
var ErrTooLarge = errTooLarge{}

type errTooLarge struct{}

func (errTooLarge) Error() string { return "edit distance exceeds limit" }

// Steps 返回最近一次 Diff 在对角线上前进（含 snake 逐行比较）的总步数。
func Steps() int64 { return steps }

var steps int64

// Diff 返回把 a 变成 b 的最短编辑脚本；D 超过 maxD 时返回 ErrTooLarge。
func Diff(a, b []lines.Line, maxD int) ([]Op, error) {
	steps = 0
	n, m := len(a), len(b)
	max := n + m
	if maxD >= 0 && maxD < max {
		max = maxD
	}
	type snap struct{ v []int }
	var trace []map[int]int
	v := map[int]int{1: 0}
	var found bool
	var fx, fy int
loop:
	for d := 0; d <= max; d++ {
		snapshot := make(map[int]int, len(v))
		for k, x := range v {
			snapshot[k] = x
		}
		trace = append(trace, snapshot)
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[k-1] < v[k+1]) {
				x = v[k+1] // 向右：插入
			} else {
				x = v[k-1] + 1 // 向下：删除（并列时优先删除）
			}
			y := x - k
			for x < n && y < m {
				steps++
				if string(a[x].Bytes()) != string(b[y].Bytes()) {
					break
				}
				x, y = x+1, y+1
			}
			v[k] = x
			if x >= n && y >= m {
				found, fx, fy = true, x, y
				break loop
			}
		}
	}
	if !found {
		return nil, ErrTooLarge
	}
	// 回溯
	ops := make([]Op, 0)
	x, y := fx, fy
	for d := len(trace) - 1; d > 0; d-- {
		vs := trace[d]
		k := x - y
		var pk int
		if k == -d || (k != d && vs[k-1] < vs[k+1]) {
			pk = k + 1
		} else {
			pk = k - 1
		}
		px := vs[pk]
		py := px - pk
		for x > px && y > py {
			x, y = x-1, y-1 // 相等行
		}
		if x == px {
			ops = append(ops, Op{Kind: Ins, Ai: x, Bi: py, Line: b[py]})
		} else {
			ops = append(ops, Op{Kind: Del, Ai: px, Bi: y, Line: a[px]})
		}
		x, y = px, py
	}
	for x > 0 && y > 0 {
		x, y = x-1, y-1
	}
	for i, j := 0, len(ops)-1; i < j; i, j = i+1, j-1 {
		ops[i], ops[j] = ops[j], ops[i]
	}
	return ops, nil
}
