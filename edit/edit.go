// Package edit 计算两个行序列之间的最短编辑脚本。
// 算法为 Myers O(ND) 线性空间变体（middle snake 分治），
// 并列最短路径固定优先删除（见 DESIGN.md 第 3 节）。
package edit

import (
	"errors"
	"sync/atomic"

	"ontology/lines"
)

// ErrTooBig 表示编辑距离超过配置上限。
var ErrTooBig = errors.New("edit: edit distance too large")

// Kind 标识编辑操作类型。
type Kind byte

const (
	Keep Kind = ' '
	Del  Kind = '-'
	Ins  Kind = '+'
)

// Op 表示一段连续同类操作，A、B 为其消耗的旧、新行数。
type Op struct {
	Kind Kind
	A, B int
}

var steps atomic.Int64

// Steps 返回最近一次 Diff 在对角线上前进的总步数（含 snake 的逐行比较）。
func Steps() int64 { return steps.Load() }

// Diff 返回把 a 变为 b 的最短编辑脚本（删除+插入行数最小）。
// maxDist > 0 时，距离超过它立即返回 ErrTooBig。
func Diff(a, b [][]byte, maxDist int) ([]Op, error) {
	steps.Store(0)
	var ops []Op
	if err := walk(a, b, maxDist, &ops); err != nil {
		return nil, err
	}
	return ops, nil
}

func walk(a, b [][]byte, maxDist int, ops *[]Op) error {
	n, m := len(a), len(b)
	p := 0
	for p < n && p < m && lines.Equal(a[p], b[p]) {
		p++
	}
	s := 0
	for s < n-p && s < m-p && lines.Equal(a[n-1-s], b[m-1-s]) {
		s++
	}
	steps.Add(int64(p + s))
	if p > 0 {
		*ops = append(*ops, Op{Keep, p, p})
	}
	if err := middle(a[p:n-s], b[p:m-s], maxDist, ops); err != nil {
		return err
	}
	if s > 0 {
		*ops = append(*ops, Op{Keep, s, s})
	}
	return nil
}

func middle(a, b [][]byte, maxDist int, ops *[]Op) error {
	n, m := len(a), len(b)
	switch {
	case n == 0:
		if m > 0 {
			*ops = append(*ops, Op{Ins, 0, m})
		}
		return nil
	case m == 0:
		*ops = append(*ops, Op{Del, n, 0})
		return nil
	}
	x0, y0, x1, y1, ok := middleSnake(a, b, maxDist)
	if !ok {
		return ErrTooBig
	}
	if err := walk(a[:x0], b[:y0], maxDist, ops); err != nil {
		return err
	}
	if x1 > x0 {
		*ops = append(*ops, Op{Keep, x1 - x0, x1 - x0})
	}
	return walk(a[x1:], b[y1:], maxDist, ops)
}

// middleSnake 找 a、b 间的中间 snake，返回其两端坐标；ok=false 表示距离超上限。
// 反向 pass 在反转下标上做标准前向贪心，避免越界且与正向共用并列规则。
func middleSnake(a, b [][]byte, maxDist int) (x0, y0, x1, y1 int, ok bool) {
	n, m := len(a), len(b)
	delta := n - m
	off := n + m + 1
	vf := make([]int, 2*off+1)
	vb := make([]int, 2*off+1)
	limit := (n + m + 1) / 2
	if maxDist > 0 && (maxDist+1)/2 < limit {
		limit = (maxDist + 1) / 2
	}
	for d := 0; d <= limit; d++ {
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && vf[off+k-1] < vf[off+k+1]) {
				x = vf[off+k+1]
			} else {
				x = vf[off+k-1] + 1
			}
			y := x - k
			sx, sy := x, y
			for x < n && y < m && lines.Equal(a[x], b[y]) {
				x++
				y++
				steps.Add(1)
			}
			steps.Add(1)
			vf[off+k] = x
			if delta%2 != 0 {
				if kr := delta - k; kr >= -(d-1) && kr <= d-1 && x+vb[off+kr] >= n {
					return sx, sy, x, y, true
				}
			}
		}
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && vb[off+k-1] < vb[off+k+1]) {
				x = vb[off+k+1]
			} else {
				x = vb[off+k-1] + 1
			}
			y := x - k
			sx, sy := x, y
			for x < n && y < m && lines.Equal(a[n-1-x], b[m-1-y]) {
				x++
				y++
				steps.Add(1)
			}
			steps.Add(1)
			vb[off+k] = x
			if delta%2 == 0 {
				if ko := delta - k; ko >= -d && ko <= d && vf[off+ko]+x >= n {
					return n - x, m - y, n - sx, m - sy, true
				}
			}
		}
	}
	return 0, 0, 0, 0, false
}
