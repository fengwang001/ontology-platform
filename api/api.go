// Package api 对外提供凸多边形交集：构造校验、求交与自检，依赖 cint。
package api

import (
	"errors"
	"fmt"

	"ontology/cint"
	"ontology/cvex"
)

// Point 为平面点。
type Point = cvex.Point

// 可判定哨兵错误，三类故障互不相同（顶点重复、非逆时针并入 ErrNotConvex）。
var (
	ErrTooFewVertices = errors.New("api: 顶点数不足（<3）")
	ErrOutOfRange     = errors.New("api: 坐标越界（|X|或|Y|>1e4）")
	ErrNotConvex      = errors.New("api: 多边形非凸/非法（非凸、自交、顶点重复或非逆时针）")
)

const maxCoord, eps = 1e4, 1e-9

// Polygon 为校验过的凸多边形，顶点逆时针，构造后不可变；空顶点表示空集。
type Polygon struct{ verts []Point }

// NewPolygon 校验顶点数、坐标范围、无重复、严格凸且逆时针；非法输入整体失败。
func NewPolygon(verts []Point) (*Polygon, error) {
	if len(verts) < 3 {
		return nil, ErrTooFewVertices
	}
	for i, v := range verts {
		if max(v.X, -v.X) > maxCoord || max(v.Y, -v.Y) > maxCoord {
			return nil, ErrOutOfRange
		}
		for j := i + 1; j < len(verts); j++ {
			if verts[i] == verts[j] {
				return nil, ErrNotConvex
			}
		}
		if cvex.Orient(v, verts[(i+1)%len(verts)], verts[(i+2)%len(verts)]) <= 0 {
			return nil, ErrNotConvex
		}
	}
	return &Polygon{append([]Point(nil), verts...)}, nil
}

// Vertices 返回顶点副本（逆时针），空多边形返回 nil。
func (p *Polygon) Vertices() []Point {
	if p.Empty() {
		return nil
	}
	return append([]Point(nil), p.verts...)
}

// Empty 报告是否为空（交集为空或面积为零）。
func (p *Polygon) Empty() bool { return p == nil || len(p.verts) == 0 }

// Intersect 返回 p∩other；nil/空参与方、空或退化（面积为零）的交集都得到空多边形。
func (p *Polygon) Intersect(other *Polygon) (*Polygon, error) {
	if p.Empty() || other.Empty() {
		return &Polygon{}, nil
	}
	var c cint.Clipper
	return &Polygon{c.Intersect(p.verts, other.verts)}, nil
}

// inside 报告 (x,y) 是否在凸多边形所有边左侧（tol 为容差，严格内部用正 tol）。
func inside(x, y float64, poly []Point, tol float64) bool {
	for i := range poly {
		if cvex.Orient(poly[i], poly[(i+1)%len(poly)], Point{X: x, Y: y}) < tol {
			return false
		}
	}
	return true
}

// SelfCheck 对内置多边形对核验四条不变量，全部通过返回 nil。
func SelfCheck() error {
	sq := func(x0, y0, x1, y1 float64) []Point {
		return []Point{{X: x0, Y: y0}, {X: x1, Y: y0}, {X: x1, Y: y1}, {X: x0, Y: y1}}
	}
	pairs := []struct{ a, b, want []Point }{
		{sq(0, 0, 4, 4), sq(2, 2, 6, 6), sq(2, 2, 4, 4)}, // P1
		{sq(0, 0, 2, 2), sq(3, 3, 5, 5), nil},            // P2
		{[]Point{{X: 0, Y: 0}, {X: 4, Y: 0}, {X: 0, Y: 4}}, sq(1, 1, 5, 5), []Point{{X: 1, Y: 1}, {X: 3, Y: 1}, {X: 1, Y: 3}}}, // P3
		{sq(0, 0, 4, 2), sq(2, 0, 6, 4), sq(2, 0, 4, 2)},                                                                       // P4
		{sq(0, 0, 4, 4), sq(4, 0, 8, 4), nil},                                                                                  // P5
	}
	for i, pr := range pairs {
		a, ea := NewPolygon(pr.a)
		b, eb := NewPolygon(pr.b)
		r, err := a.Intersect(b)
		if ea != nil || eb != nil || err != nil {
			return fmt.Errorf("selfcheck#%d 构造失败: %v %v %v", i, ea, eb, err)
		}
		got := r.Vertices()
		if !samePoly(got, pr.want) { // 不变量1：与朴素裁剪一致
			return fmt.Errorf("selfcheck#%d: 交集 %v != 朴素结果 %v", i, got, pr.want)
		}
		if err := inv23(got, pr.a, pr.b); err != nil { // 不变量2、3
			return fmt.Errorf("selfcheck#%d: %w", i, err)
		}
	}
	bads := [][]Point{{{X: 0, Y: 0}, {X: 1, Y: 0}}, {{X: 0, Y: 0}, {X: 2e4, Y: 0}, {X: 0, Y: 1}}, {{X: 0, Y: 0}, {X: 4, Y: 0}, {X: 2, Y: 1}, {X: 0, Y: 4}}} // 不变量4：被拒输入整体失败，不留部分结果
	for i, bv := range bads {
		if p, err := NewPolygon(bv); err == nil || p != nil {
			return fmt.Errorf("selfcheck bad#%d: 未整体失败", i)
		}
	}
	return nil
}

// inv23 核验不变量2（结果严格凸、顶点满足双方全部半平面）与不变量3（覆盖正确）。
func inv23(got, a, b []Point) error {
	for i, v := range got {
		if cvex.Orient(v, got[(i+1)%len(got)], got[(i+2)%len(got)]) <= eps {
			return errors.New("结果非严格凸")
		}
		if !inside(v.X, v.Y, a, -eps) || !inside(v.X, v.Y, b, -eps) {
			return fmt.Errorf("顶点 %v 越出半平面", v)
		}
	}
	for x := -1.0; x <= 9; x += 0.5 {
		for y := -1.0; y <= 9; y += 0.5 {
			if inside(x, y, a, eps) && inside(x, y, b, eps) && !inside(x, y, got, -eps) {
				return fmt.Errorf("严格内部点 (%v,%v) 未被覆盖", x, y)
			}
		}
	}
	return nil
}

// samePoly 报告两顶点序列是否同一多边形（允许旋转起点，容差 eps）。
func samePoly(a, b []Point) bool {
	if len(a) != len(b) {
		return false
	}
next:
	for s := range a {
		for i := range a {
			if dx, dy := a[(s+i)%len(a)].X-b[i].X, a[(s+i)%len(a)].Y-b[i].Y; dx*dx+dy*dy > eps*eps {
				continue next
			}
		}
		return true
	}
	return len(a) == 0
}
