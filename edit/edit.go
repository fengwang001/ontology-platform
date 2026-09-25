// Package edit 在两个行序列间求最短编辑脚本（前向 Myers，删除优先）。
package edit

import (
	"errors"

	"ontology/lines"
)

// Kind 是编辑操作类型。
type Kind uint8

const (
	Equal Kind = iota // 公共行
	Delete            // 仅在旧序列
	Insert            // 仅在新序列
)

// Op 是一条编辑操作。
type Op struct {
	Kind Kind
	A    lines.Line // Delete/Equal 时来自旧序列
	B    lines.Line // Insert/Equal 时来自新序列
}

// ErrTooDifferent 表示编辑距离超过 MaxDist 上限（差异过大）。
var ErrTooDifferent = errors.New("edit: edit distance exceeds limit")

// Differ 保存最近一次差分的参数与内部计数。
type Differ struct {
	// MaxDist 为编辑距离（删除+插入）上限，<=0 表示不限制。
	MaxDist int
	steps    int // 最近一次对角线前进步数（含 snake 逐行比较）
}

// Steps 返回最近一次 Diff 在对角线上前进的总步数。
func (d *Differ) Steps() int { return d.steps }

// Diff 返回 a→b 的最短编辑脚本；距离超 MaxDist 返回 ErrTooDifferent。
func (d *Differ) Diff(a, b []lines.Line) ([]Op, error) {
	n, m := len(a), len(b)
	max := n + m
	if d.MaxDist > 0 && d.MaxDist < max {
		max = d.MaxDist
	}
	d.steps = 0
	v := map[int]int{1: 0}
	trace := []map[int]int{cloneV(v)}
	var found bool
	var endX int
search:
	for dd := 0; dd <= max; dd++ {
		for k := -dd; k <= dd; k += 2 {
			var x int
			switch {
			case k == -dd:
				x = v[k+1]
			case k == dd:
				x = v[k-1] + 1
			default:
				down, right := v[k+1], v[k-1]
				if down < right { // 删除优先：k-1 更优才向下
					x = down
				} else {
					x = right + 1
				}
			}
			y := x - k
			for x < n && y < m {
				d.steps++ // snake 内逐行比较
				if a[x].Data != b[y].Data {
					break
				}
				x++
				y++
			}
			v[k] = x
			d.steps++ // 本对角线上前进一步
			if x >= n && y >= m {
				found, endX = true, x
				break search
			}
		}
		trace = append(trace, cloneV(v))
	}
	if !found {
		return nil, ErrTooDifferent
	}
	ops := d.backtrack(a, b, trace, endX, m)
	return ops, nil
}

func (d *Differ) backtrack(a, b []lines.Line, trace []map[int]int, ex, ey int) []Op {
	var rev []Op
	x, y := ex, ey
	for dd := len(trace) - 1; dd > 0; dd-- {
		v := trace[dd-1]
		k := x - y
		var pk int
		switch {
		case k == -dd:
			pk = k + 1
		case k == dd:
			pk = k - 1
		default:
			if v[k+1] < v[k-1] {
				pk = k
			} else {
				pk = k - 1
			}
		}
		px, py := v[pk], v[pk]-pk
		for x > px+boolToInt(pk == k-1) && y > py+boolToInt(pk == k+1) {
			rev = append(rev, Op{Kind: Equal, A: a[x-1], B: b[y-1]})
			x--
			y--
		}
		if dd > 0 {
			if pk == k-1 {
				rev = append(rev, Op{Kind: Delete, A: a[x-1]})
				x--
			} else {
				rev = append(rev, Op{Kind: Insert, B: b[y-1]})
				y--
			}
		}
	}
	for x > 0 && y > 0 {
		rev = append(rev, Op{Kind: Equal, A: a[x-1], B: b[y-1]})
		x--
		y--
	}
	ops := make([]Op, len(rev))
	for i, op := range rev {
		ops[len(rev)-1-i] = op
	}
	return ops
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func cloneV(v map[int]int) map[int]int {
	c := make(map[int]int, len(v)+2)
	for k, x := range v {
		c[k] = x
	}
	return c
}
