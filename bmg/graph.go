// Package bmg 持有带权完全二分图：权矩阵、SetWeight 与下标/重复校验。
// 本包不依赖其他包。
package bmg

import "errors"

// 哨兵错误：可判定、互不相同。
var (
	// ErrNodeRange 引用了 [0,n) 之外的节点。
	ErrNodeRange = errors.New("bmg: node index out of range")
	// ErrDuplicate 对同一 (l,r) 重复设置权值。
	ErrDuplicate = errors.New("bmg: weight already set")
)

// Graph 是左右各 n 个节点的完全二分图。零值不可用，须经 New 构造。
type Graph struct {
	n  int
	w  [][]int64 // w[l][r]：权值
	ok [][]bool  // ok[l][r]：该格是否已设置
}

// New 构造左右各 n 个节点的空图。调用方须保证 n>0。
func New(n int) *Graph {
	g := &Graph{n: n}
	g.w = make([][]int64, n)
	g.ok = make([][]bool, n)
	for i := range g.w {
		g.w[i] = make([]int64, n)
		g.ok[i] = make([]bool, n)
	}
	return g
}

// N 返回每侧节点数。
func (g *Graph) N() int { return g.n }

// SetWeight 设置左 l 与右 r 之间的权。
// 越界返回 ErrNodeRange，重复设置返回 ErrDuplicate；
// 任一校验失败都在写入前返回，因此被拒操作绝不改变矩阵（失败不留痕）。
func (g *Graph) SetWeight(l, r int, w int64) error {
	if l < 0 || l >= g.n || r < 0 || r >= g.n {
		return ErrNodeRange
	}
	if g.ok[l][r] {
		return ErrDuplicate
	}
	// 全部校验通过后才落盘。
	g.w[l][r] = w
	g.ok[l][r] = true
	return nil
}

// Weight 返回 (l,r) 的权与是否已设置；越界视为未设置。
func (g *Graph) Weight(l, r int) (int64, bool) {
	if l < 0 || l >= g.n || r < 0 || r >= g.n {
		return 0, false
	}
	return g.w[l][r], g.ok[l][r]
}

// Complete 报告 n*n 个权是否全部已设置。
func (g *Graph) Complete() bool {
	for i := 0; i < g.n; i++ {
		for j := 0; j < g.n; j++ {
			if !g.ok[i][j] {
				return false
			}
		}
	}
	return true
}

// Clone 深拷贝，使求解可在与写入互斥之外独立进行。
func (g *Graph) Clone() *Graph {
	c := &Graph{n: g.n}
	c.w = make([][]int64, g.n)
	c.ok = make([][]bool, g.n)
	for i := range g.w {
		c.w[i] = append([]int64(nil), g.w[i]...)
		c.ok[i] = append([]bool(nil), g.ok[i]...)
	}
	return c
}
