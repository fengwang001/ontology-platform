// Package api 对外提供简单多边形及其并集，依赖 un。
package api

import (
	"errors"
	"slices"

	"ontology/poly"
	"ontology/un"
)

// 四类可判定故障的哨兵错误（互不相同）；ErrInvariant 为自检失败。
var (
	ErrTooFewVertices = errors.New("api: fewer than 3 vertices")
	ErrOutOfRange     = errors.New("api: coordinate |X| or |Y| exceeds 1e4")
	ErrDuplicate      = errors.New("api: duplicate adjacent vertex")
	ErrClockwise      = errors.New("api: vertices not counter-clockwise")
	ErrSelfIntersect  = errors.New("api: polygon self-intersects")
	ErrInvariant      = errors.New("api: invariant violated")
)

// Polygon 为校验过的简单多边形，构造后不可变，可并发只读。
type Polygon struct{ verts []poly.Point }

// NewPolygon 校验并构造简单多边形；任何拒绝都不产生部分结果。
func NewPolygon(verts []poly.Point) (*Polygon, error) {
	if err := validate(verts); err != nil {
		return nil, err
	}
	return &Polygon{slices.Clone(verts)}, nil
}

func validate(v []poly.Point) error {
	if len(v) < 3 {
		return ErrTooFewVertices
	}
	for i := range v {
		if a := v[i]; a.X > 1e4 || a.X < -1e4 || a.Y > 1e4 || a.Y < -1e4 {
			return ErrOutOfRange
		}
		if v[i] == v[(i+1)%len(v)] {
			return ErrDuplicate
		}
	}
	if poly.Area2(v) <= 0 {
		return ErrClockwise
	}
	if selfIntersects(v) {
		return ErrSelfIntersect
	}
	return nil
}

func selfIntersects(v []poly.Point) bool {
	n := len(v)
	for i := 0; i < n; i++ {
		for j := i + 2; j < n && !(i == 0 && j == n-1); j++ {
			if poly.SegCross(v[i], v[(i+1)%n], v[j], v[(j+1)%n]) {
				return true
			}
		}
	}
	return false
}

// Union 返回 p ∪ other 的并集多边形（只算并，不算交/差）。
func (p *Polygon) Union(other *Polygon) (*Polygon, error) {
	vs, err := un.Union(p.verts, other.verts)
	if err != nil {
		return nil, err
	}
	return &Polygon{vs}, nil
}

// Vertices 返回顶点副本（逆时针），并发安全。
func (p *Polygon) Vertices() []poly.Point { return slices.Clone(p.verts) }

// SelfCheck 校验结果自洽：无自交、无相邻重复、逆时针、面积非负。并发安全。
func (p *Polygon) SelfCheck() error { return validate(p.verts) }

// SelfTest 对内置多边形对核验四条不变量，可被测试直接调用。
func SelfTest() error {
	rect := func(x0, y0, x1, y1 float64) []poly.Point {
		return []poly.Point{{X: x0, Y: y0}, {X: x1, Y: y0}, {X: x1, Y: y1}, {X: x0, Y: y1}}
	}
	pairs := [][2][]poly.Point{
		{rect(0, 0, 3, 3), rect(2, 2, 5, 5)},
		{rect(0, 0, 6, 6), rect(1, 1, 2, 2)},
		{rect(0, 0, 4, 4), rect(3, 3, 5, 5)},
	}
	for _, pr := range pairs {
		A, _ := NewPolygon(pr[0])
		B, _ := NewPolygon(pr[1])
		U, err := A.Union(B)
		if err != nil {
			return err
		}
		if err := U.SelfCheck(); err != nil { // 不变量2：结果自洽
			return err
		}
		want := poly.Area2(pr[0])/2 + poly.Area2(pr[1])/2 - rectInter(pr[0], pr[1]) // 不变量1
		if got := poly.Area2(U.verts) / 2; got < want-1e-9 || got > want+1e-9 {
			return ErrInvariant
		}
		if !pointsOK(U.verts, pr[0], pr[1]) { // 不变量2b/3：顶点归属 + 覆盖
			return ErrInvariant
		}
	}
	if p, err := NewPolygon([]poly.Point{{X: 0, Y: 0}, {X: 1, Y: 0}}); err == nil || p != nil { // 不变量4
		return ErrInvariant
	}
	return nil
}

// rectInter 对照实现：轴对齐矩形的交集面积（内置对均为矩形）。
func rectInter(a, b []poly.Point) float64 {
	dx := min(a[2].X, b[2].X) - max(a[0].X, b[0].X)
	dy := min(a[2].Y, b[2].Y) - max(a[0].Y, b[0].Y)
	return max(0, dx) * max(0, dy)
}

// pointsOK 核验顶点归属（在边界上或严格在内）与采样点双向覆盖一致。
func pointsOK(u, a, b []poly.Point) bool {
	for _, v := range append(a, b...) {
		on := false
		for i := range u {
			if on = poly.OnSeg(u[i], u[(i+1)%len(u)], v); on {
				break
			}
		}
		if !on && !poly.PointInPoly(v, u) {
			return false
		}
	}
	minx, miny, maxx, maxy := u[0].X, u[0].Y, u[0].X, u[0].Y
	for _, v := range u {
		minx, miny, maxx, maxy = min(minx, v.X), min(miny, v.Y), max(maxx, v.X), max(maxy, v.Y)
	}
	for i := 1; i < 20; i++ {
		for j := 1; j < 20; j++ {
			p := poly.Point{X: minx + float64(i)*(maxx-minx)/20, Y: miny + float64(j)*(maxy-miny)/20}
			if poly.PointInPoly(p, u) != (poly.PointInPoly(p, a) || poly.PointInPoly(p, b)) {
				return false
			}
		}
	}
	return true
}
