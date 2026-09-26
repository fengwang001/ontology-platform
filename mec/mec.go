// Package mec 用 Welzl 算法维护最小包围圆，依赖 circ。
package mec

import (
	"math/big"

	"ontology/circ"
)

// MEC 增量维护点集的最小包围圆。
// checks 是非导出计数器：最近一次 Insert 执行的「点在圆内」判定次数。
type MEC struct {
	pts    []circ.Point
	c      circ.Circle
	has    bool
	checks int
}

// New 返回空的 MEC。
func New() *MEC { return &MEC{} }

// Insert 加入一个点；若已在当前圆内则 O(1) 返回，否则 Welzl 重建。
func (m *MEC) Insert(p circ.Point) {
	m.checks = 0
	if m.has {
		m.checks++
		if m.c.Contains(p) {
			m.pts = append(m.pts, p)
			return
		}
	}
	m.pts = append(m.pts, p)
	m.c = m.welzl(m.pts, nil, len(m.pts))
	m.has = true
}

// Circle 返回当前最小包围圆；空集时 ok=false。
func (m *MEC) Circle() (c circ.Circle, ok bool) { return m.c, m.has }

// Points 返回已插入点的副本。
func (m *MEC) Points() []circ.Point { return append([]circ.Point(nil), m.pts...) }

// welzl 递归：p 前 n 个点为待处理集，r 为边界集。
func (m *MEC) welzl(p []circ.Point, r []circ.Point, n int) circ.Circle {
	if n == 0 || len(r) == 3 {
		return trivial(r)
	}
	c := m.welzl(p, r, n-1)
	m.checks++
	if c.Contains(p[n-1]) {
		return c
	}
	return m.welzl(p, append(append([]circ.Point(nil), r...), p[n-1]), n-1)
}

// trivial 由 0~3 个边界点直接确定圆。
func trivial(r []circ.Point) circ.Circle {
	switch len(r) {
	case 1:
		return circ.FromPoint(r[0])
	case 2:
		return circ.FromDiameter(r[0], r[1])
	case 3:
		return circ.FromThree(r[0], r[1], r[2])
	}
	// 空边界：返回 r²=-1 的哨兵圆，任何点都不在圆内。
	return circ.Circle{Cx: big.NewRat(0, 1), Cy: big.NewRat(0, 1), R2: big.NewRat(-1, 1)}
}

// BruteForce 枚举所有点对（直径圆）与三元组（外接圆，钝角/共线回退），
// 取能覆盖全部点的最小圆，作为正确性基准。
func BruteForce(pts []circ.Point) circ.Circle {
	var best circ.Circle
	has := false
	consider := func(c circ.Circle) {
		for _, p := range pts {
			if !c.Contains(p) {
				return
			}
		}
		if !has || c.R2.Cmp(best.R2) < 0 {
			best, has = c, true
		}
	}
	for _, p := range pts {
		consider(circ.FromPoint(p))
	}
	for i := range pts {
		for j := i + 1; j < len(pts); j++ {
			consider(circ.FromDiameter(pts[i], pts[j]))
		}
	}
	for i := range pts {
		for j := i + 1; j < len(pts); j++ {
			for k := j + 1; k < len(pts); k++ {
				consider(circ.FromThree(pts[i], pts[j], pts[k]))
			}
		}
	}
	return best
}
