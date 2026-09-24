// Package edit 计算两个行序列之间的最短编辑脚本（Myers O(ND)）。
package edit

import (
	"ontology/lines"
)

// Op 为一个编辑操作；Kind 为 ' '（相等）、'-'（仅旧）、'+'（仅新）。
type Op struct {
	Kind byte
	Old  int
	New  int
}

// ErrTooDifferent 是编辑距离超过上限的哨兵错误。
var ErrTooDifferent = errDiff("edit distance exceeds limit")

type errDiff string

func (e errDiff) Error() string { return string(e) }

type frontier struct {
	x    int
	prev int
}

// Options 配置差分；MaxD 为删除行数+插入行数的上限，<=0 不限。
type Options struct {
	MaxD int
}

// Differ 保存最近一次差分的非导出计数器。
type Differ struct {
	steps int
}

// Steps 返回最近一次差分蛇形逐行比较的总次数。
func (d *Differ) Steps() int { return d.steps }

// Diff 计算最短编辑脚本。
// 并列最短路径时优先删除（见 DESIGN.md 第 3 节）。
func (d *Differ) Diff(a, b []lines.Line, opts Options) ([]Op, error) {
	n, m := len(a), len(b)
	max := n + m
	if opts.MaxD > 0 && opts.MaxD < max {
		max = opts.MaxD
	}
	d.steps = 0
	v := map[int]frontier{}
	var trace []map[int]frontier
	var found bool
	for dd := 0; dd <= max; dd++ {
		cur := map[int]frontier{}
		for k := -dd; k <= dd; k += 2 {
			var x int
			down := k == -dd || (k != dd && v[k+1].x >= v[k-1].x)
			if down {
				x = v[k+1].x
			} else {
				x = v[k-1].x + 1
			}
			y := x - k
			for x < n && y < m {
				d.steps++
				if !lines.Equal(a[x], b[y]) {
					break
				}
				x, y = x+1, y+1
			}
			cur[k] = frontier{x: x, prev: bb(down, k+1, k-1)}
			if x >= n && y >= m {
				found = true
				break
			}
		}
		trace = append(trace, cur)
		v = cur
		if found {
			break
		}
	}
	if !found {
		return nil, ErrTooDifferent
	}
	return backtrack(a, b, trace), nil
}

func bb(down bool, dk, ik int) int {
	if down {
		return dk
	}
	return ik
}

func backtrack(a, b []lines.Line, trace []map[int]frontier) []Op {
	x, y := len(a), len(b)
	var ops []Op
	for dd := len(trace) - 1; dd >= 0; dd-- {
		v := trace[dd]
		k := x - y
		pk := v[k].prev
		var px, py int
		if dd > 0 {
			f := trace[dd-1][pk]
			px, py = f.x, f.x-pk
		}
		for x > px && y > py {
			ops = append(ops, Op{Kind: ' ', Old: x - 1, New: y - 1})
			x, y = x-1, y-1
		}
		if x > px {
			ops = append(ops, Op{Kind: '-', Old: x - 1, New: -1})
			x--
		} else if y > py {
			ops = append(ops, Op{Kind: '+', Old: -1, New: y - 1})
			y--
		}
	}
	return reverseOps(ops)
}

func reverseOps(ops []Op) []Op {
	out := make([]Op, len(ops))
	for i, op := range ops {
		out[len(ops)-1-i] = op
	}
	return out
}
