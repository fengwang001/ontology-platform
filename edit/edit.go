// Package edit 计算两个行序列之间的最短编辑脚本（Myers O(ND)）。
package edit

import (
	"errors"

	"ontology/lines"
)

// Kind 是脚本操作种类。
type Kind uint8

const (
	Equal  Kind = iota // 保留行
	Delete             // 只在旧序列中的行
	Insert             // 只在新序列中的行
)

// Op 是一条编辑操作。Delete 时 L 为旧行，Insert 时为新行。
type Op struct {
	Kind Kind
	L    lines.Line
}

// ErrTooDifferent 是编辑距离超过上限的可判定错误。
var ErrTooDifferent = errors.New("edit: edit distance exceeds limit")

// steps 记录最近一次 Diff 在对角线上前进的总步数（含 snake 内逐行比较）。
var steps int

// Steps 返回最近一次 Diff 的对角线步数。
func Steps() int { return steps }

// Diff 返回 a 到 b 的最短脚本。并列最短路径固定优先删除（见 DESIGN.md 3）。
// maxD 为可接受的编辑距离上限（删除数+插入数），超过立即返回 ErrTooDifferent。
func Diff(a, b []lines.Line, maxD int) ([]Op, error) {
	n, m := len(a), len(b)
	steps = 0
	if maxD < 0 || maxD > n+m {
		maxD = n + m
	}
	off := maxD + 1
	size := 2*off + 1
	v := make([]int, size)
	trace := make([][]int, 0, maxD+1)
	for d := 0; d <= maxD; d++ {
		snap := make([]int, size)
		copy(snap, v)
		trace = append(trace, snap)
		for k := -d; k <= d; k += 2 {
			ki := k + off
			var x int
			if k == -d || (k != d && v[ki-1] < v[ki+1]) {
				x = v[ki+1] // 向右：插入
			} else {
				x = v[ki-1] + 1 // 向下：删除（并列时走这里，优先删除）
			}
			y := x - k
			for x < n && y < m && eq(a[x], b[y]) {
				steps++ // snake 内一次对角线比较/前进一步
				x++
				y++
			}
			v[ki] = x
			if x >= n && y >= m {
				ops := backtrack(a, b, trace, off)
				return ops, nil
			}
		}
	}
	return nil, ErrTooDifferent
}

// Distance 返回脚本的删除行数加插入行数。
func Distance(ops []Op) int {
	d := 0
	for _, op := range ops {
		if op.Kind != Equal {
			d++
		}
	}
	return d
}

func backtrack(a, b []lines.Line, trace [][]int, off int) []Op {
	ops := make([]Op, 0, len(a)+len(b))
	x, y := len(a), len(b)
	for d := len(trace) - 1; d > 0; d-- {
		v := trace[d]
		k := x - y
		ki := k + off
		var pk int
		if k == -d || (k != d && v[ki-1] < v[ki+1]) {
			pk = k + 1
		} else {
			pk = k - 1
		}
		px := v[pk+off]
		py := px - pk
		for x > px && y > py {
			ops = append(ops, Op{Kind: Equal, L: a[x-1]})
			x--
			y--
		}
		if x == px {
			ops = append(ops, Op{Kind: Insert, L: b[y-1]})
			y--
		} else {
			ops = append(ops, Op{Kind: Delete, L: a[x-1]})
			x--
		}
	}
	v := trace[0]
	for x > v[off] {
		ops = append(ops, Op{Kind: Equal, L: a[x-1]})
		x--
	}
	for i, j := 0, len(ops)-1; i < j; i, j = i+1, j-1 {
		ops[i], ops[j] = ops[j], ops[i]
	}
	return ops
}

func eq(a, b lines.Line) bool { return a.Content() == b.Content() }
