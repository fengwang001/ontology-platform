// Package edit 在两个行序列间求最短编辑脚本（前向 Myers，O(ND)）。
package edit

import (
	"errors"

	"ontology/lines"
)

// Op 是单个编辑操作。
type Op struct {
	Kind byte // '=' 保留, '-' 删除旧行, '+' 插入新行
	Old  int  // Kind 为 '='/'-' 时的旧行下标，否则 -1
	New  int  // Kind 为 '='/'+' 时的新行下标，否则 -1
}

// ErrTooDifferent 表示编辑距离超过调用方给出的上限。
var ErrTooDifferent = errors.New("edit: difference exceeds limit")

// Option 配置差分。
type Option func(*cfg)

type cfg struct{ maxDist int }

// WithMaxDistance 设置删除数+插入数上限；超过即返回 ErrTooDifferent。
func WithMaxDistance(d int) Option {
	return func(c *cfg) { c.maxDist = d }
}

// 最近一次 Diff 在对角线上推进时进行的逐行比较总次数（含 snake）。
var compareCount int

// LastCompares 返回最近一次 Diff 的对角线比较步数，供复杂度测试使用。
func LastCompares() int { return compareCount }

type snap struct{ v []int }

// Diff 返回 a→b 的最短编辑脚本。maxDist<0 表示不限距离。
func Diff(a, b []lines.Line, opts ...Option) ([]Op, error) {
	compareCount = 0
	c := cfg{maxDist: -1}
	for _, o := range opts {
		o(&c)
	}
	n, m := len(a), len(b)
	v := make([]int, 2*(n+m)+3)
	off := n + m + 1
	at := func(k int) int { return v[k+off] }
	set := func(k, x int) { v[k+off] = x }
	set(1, 0)
	var trace []snap
	found := false
	maxD := n + m
	for d := 0; d <= maxD; d++ {
		if c.maxDist >= 0 && d > c.maxDist {
			return nil, ErrTooDifferent
		}
		trace = append(trace, snap{v: append([]int(nil), v...)})
		for k := -d; k <= d; k += 2 {
			var x int
			switch {
			case k == -d:
				x = at(k + 1)
			case k == d:
				x = at(k-1) + 1
			case at(k-1) < at(k+1):
				x = at(k + 1)
			default:
				x = at(k-1) + 1
			}
			y := x - k
			for x < n && y < m {
				compareCount++
				if !lines.Equal(a[x], b[y]) {
					break
				}
				x, y = x+1, y+1
			}
			set(k, x)
			if x >= n && y >= m {
				found = true
				break
			}
		}
		if found {
			break
		}
	}
	return backtrack(a, b, trace, off), nil
}

func backtrack(a, b []lines.Line, trace []snap, off int) []Op {
	x, y := len(a), len(b)
	var rev []Op
	for d := len(trace) - 1; d > 0; d-- {
		v := trace[d].v
		k := x - y
		var pk int
		switch {
		case k == -d:
			pk = k + 1
		case k == d:
			pk = k - 1
		case v[k-1+off] < v[k+1+off]:
			pk = k + 1
		default:
			pk = k - 1
		}
		px := v[pk+off]
		py := px - pk
		for x > px && y > py {
			rev = append(rev, Op{Kind: '=', Old: x - 1, New: y - 1})
			x, y = x-1, y-1
		}
		if d > 0 {
			if x == px {
				rev = append(rev, Op{Kind: '+', Old: -1, New: y - 1})
				y--
			} else {
				rev = append(rev, Op{Kind: '-', Old: x - 1, New: -1})
				x--
			}
		}
	}
	v := trace[0].v
	for x > v[off] {
		rev = append(rev, Op{Kind: '=', Old: x - 1, New: y - 1})
		x, y = x-1, y-1
	}
	out := make([]Op, len(rev))
	for i := range rev {
		out[i] = rev[len(rev)-1-i]
	}
	return out
}

// Distance 返回脚本的删除行数加插入行数。
func Distance(s []Op) int {
	d := 0
	for _, o := range s {
		if o.Kind != '=' {
			d++
		}
	}
	return d
}
