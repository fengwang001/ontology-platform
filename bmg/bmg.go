// Package bmg 持有带权完全二分图的权矩阵，只负责存储与校验。
package bmg

import "errors"

// 哨兵错误：构造/写入阶段的三类可判定失败。
var (
	ErrInvalidN        = errors.New("bmg: n must be positive")
	ErrNodeOutOfRange  = errors.New("bmg: node index out of [0,n)")
	ErrDuplicateWeight = errors.New("bmg: weight for (l,r) already set")
)

// Graph 是 n×n 的权矩阵；w[i][j] 零值与“未设置”靠 set[i][j] 区分。
type Graph struct {
	n   int
	w   [][]int64
	set [][]bool
	cnt int
}

// New 固定左右节点数；n 非正时整体失败、不产生任何可用状态。
func New(n int) (*Graph, error) {
	if n <= 0 {
		return nil, ErrInvalidN
	}
	w := make([][]int64, n)
	set := make([][]bool, n)
	for i := range w {
		w[i] = make([]int64, n)
		set[i] = make([]bool, n)
	}
	return &Graph{n: n, w: w, set: set}, nil
}

// N 返回节点数。
func (g *Graph) N() int { return g.n }

// SetWeight 设置左 l 与右 r 之间的权。所有校验先于任何写入：
// 越界或重复设置都不会改动矩阵（失败不留痕）。
func (g *Graph) SetWeight(l, r int, w int64) error {
	if l < 0 || l >= g.n || r < 0 || r >= g.n {
		return ErrNodeOutOfRange
	}
	if g.set[l][r] {
		return ErrDuplicateWeight
	}
	g.w[l][r] = w
	g.set[l][r] = true
	g.cnt++
	return nil
}

// Weight 读取权；第二个返回值报告该边是否已设置。
func (g *Graph) Weight(l, r int) (int64, bool) {
	if l < 0 || l >= g.n || r < 0 || r >= g.n {
		return 0, false
	}
	return g.w[l][r], g.set[l][r]
}

// Row 返回第 l 行权值切片（只读使用）。
func (g *Graph) Row(l int) []int64 { return g.w[l] }

// AllSet 报告 n×n 条边是否全部赋过值。
func (g *Graph) AllSet() bool { return g.cnt == g.n*g.n }
