// Package edit 计算两个行序列之间的最短编辑脚本（前向 Myers，O(ND)）。
package edit

import (
	"errors"

	"ontology/lines"
)

// ErrTooDifferent 表示编辑距离超过调用方给定的上限。
var ErrTooDifferent = errors.New("edit: edit distance exceeds limit")

// Op 是一段同类型编辑：Kind 为 ' '（保留）、'-'（删除）、'+'（插入）。
// A0:A1、B0:B1 分别为该段在旧、新行序列中的半开区间。
type Op struct {
	Kind   byte
	A0, A1 int
	B0, B1 int
}

// Script 是有序的最小编辑段序列。
type Script []Op

// Distance 返回删除行数与插入行数之和（编辑距离）。
func (s Script) Distance() int {
	d := 0
	for _, op := range s {
		if op.Kind != ' ' {
			d += op.A1 - op.A0 + op.B1 - op.B0
		}
	}
	return d
}

// steps 是最近一次 Diff 在对角线上前进的总步数（含 snake 的逐行比较）。
var steps int

// Steps 返回最近一次 Diff 的对角线前进总步数。
func Steps() int { return steps }

type edge struct {
	kind           byte
	ax, ay, bx, by int
}

// Diff 返回 a→b 的最短脚本。maxEdits<0 表示不限；超过上限返回 ErrTooDifferent。
// 并列最短路径固定优先删除（见 DESIGN.md 第 3 节）。
func Diff(a, b []lines.Line, maxEdits int) (Script, error) {
	steps = 0
	n, m := len(a), len(b)
	off := n + m + 1
	v := make([]int, 2*off+1)
	var trace [][]int
	found := false
	for d := 0; d <= n+m; d++ {
		if maxEdits >= 0 && d > maxEdits {
			return nil, ErrTooDifferent
		}
		snap := append([]int(nil), v...)
		trace = append(trace, snap)
		for k := -d; k <= d; k += 2 {
			steps++
			var x int
			if k == -d || (k != d && v[k-1+off] < v[k+1+off]) {
				x = v[k+1+off] // 插入
			} else {
				x = v[k-1+off] + 1 // 删除（并列优先）
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
			v[k+off] = x
			if x >= n && y >= m {
				found = true
				break
			}
		}
		if found {
			break
		}
	}
	return backtrace(a, b, trace), nil
}

func backtrace(a, b []lines.Line, trace [][]int) Script {
	n, m := len(a), len(b)
	off := n + m + 1
	x, y := n, m
	var rev []edge
	for d := len(trace) - 1; d >= 0; d-- {
		v := trace[d]
		k := x - y
		var pk int
		if k == -d || (k != d && v[k-1+off] < v[k+1+off]) {
			pk = k + 1
		} else {
			pk = k - 1
		}
		px, py := v[pk+off], v[pk+off]-pk
		for x > px && y > py {
			rev = append(rev, edge{' ', x - 1, x, y - 1, y})
			x--
			y--
		}
		if d > 0 {
			if x == px {
				rev = append(rev, edge{'+', x, x, y - 1, y})
			} else {
				rev = append(rev, edge{'-', x - 1, x, y, y})
			}
			x, y = px, py
		}
	}
	for x > 0 && y > 0 {
		rev = append(rev, edge{' ', x - 1, x, y - 1, y})
		x--
		y--
	}
	return coalesce(rev)
}

func coalesce(rev []edge) Script {
	var s Script
	for i := len(rev) - 1; i >= 0; {
		k := rev[i].kind
		op := Op{Kind: k, A0: rev[i].ax, A1: rev[i].ay, B0: rev[i].bx, B1: rev[i].by}
		for i >= 0 && rev[i].kind == k {
			if rev[i].ax < op.A0 {
				op.A0 = rev[i].ax
			}
			if rev[i].ay > op.A1 {
				op.A1 = rev[i].ay
			}
			if rev[i].bx < op.B0 {
				op.B0 = rev[i].bx
			}
			if rev[i].by > op.B1 {
				op.B1 = rev[i].by
			}
			i--
		}
		s = append(s, op)
	}
	return s
}
