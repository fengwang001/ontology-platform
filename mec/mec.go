// Package mec 用 Welzl 算法增量维护点集的最小包围圆。
package mec

import (
	"math/big"

	"ontology/circ"
)

// MEC 维护点集的最小包围圆。checks 为非导出计数器，
// 记录最近一次 Insert 执行的「点在圆内」判定次数，不出现在任何公开接口。
type MEC struct {
	pts    []circ.Point
	c      circ.Circle
	has    bool
	checks int
}

// New 返回空的 MEC。
func New() *MEC { return &MEC{} }

// Insert 增量插入一个点：点在圆内时只做一次判定，否则全量重建。
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
	m.c = welzl(m.pts, nil, &m.checks)
	// 边界恰为「确定该圆的点」：取点集中恰在圆上的点，至多 3 个
	//（圆上任意 3 点必不共线，足以确定该圆）。
	m.c.B = nil
	for _, q := range m.pts {
		if m.c.OnBoundary(q) {
			m.c.B = append(m.c.B, q)
			if len(m.c.B) == 3 {
				break
			}
		}
	}
	m.has = true
}

// Circle 返回当前最小包围圆；ok 为 false 表示点集为空。
func (m *MEC) Circle() (c circ.Circle, ok bool) { return m.c, m.has }

// trivial 返回边界点集（至多 3 点）直接确定的圆。
func trivial(b []circ.Point) circ.Circle {
	switch len(b) {
	case 1:
		return circ.FromPoint(b[0])
	case 2:
		return circ.FromDiameter(b[0], b[1])
	case 3:
		return circ.FromThree(b[0], b[1], b[2])
	}
	zero := new(big.Rat)
	return circ.Circle{Cx: zero, Cy: new(big.Rat), R2: new(big.Rat)}
}

// welzl 返回 pts 的最小包围圆，且 b 中的点必须在边界上。
// cnt 累计「点在圆内」判定次数。
func welzl(pts []circ.Point, b []circ.Point, cnt *int) circ.Circle {
	if len(pts) == 0 || len(b) == 3 {
		return trivial(b)
	}
	p, rest := pts[0], pts[1:]
	c := welzl(rest, b, cnt)
	*cnt++
	if c.Contains(p) {
		return c
	}
	return welzl(rest, append(b, p), cnt)
}

// InteriorProbe 白盒自检：先插入 (0,0)、(1000,0) 确定直径圆，再插入 m 个
// 严格落在圆内的点，报告每次插入的「点在圆内」判定次数是否恒为 1。
// 只返回布尔结论，计数器数值不从任何导出入口流出。
func InteriorProbe(m int) bool {
	w := New()
	w.Insert(circ.Point{X: 0, Y: 0})
	w.Insert(circ.Point{X: 1000, Y: 0})
	for i := 0; i < m; i++ {
		w.Insert(circ.Point{X: 450 + i%100, Y: i/100 - 50})
		if w.checks != 1 {
			return false
		}
	}
	return true
}
