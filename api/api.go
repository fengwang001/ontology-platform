// Package api 对外提供简单多边形的构造、面积、重心与自检。
// 依赖 meas；校验失败整体拒绝，不留部分结果。
package api

import (
	"errors"
	"fmt"

	"ontology/meas"
	"ontology/poly"
)

// 对外类型别名。
type (
	Point    = poly.Point
	Rat      = meas.Rat
	PointRat = meas.PointRat
)

// 可判定的哨兵错误，互不相同。
var (
	ErrTooFewVertices  = errors.New("polygon: fewer than 3 vertices")
	ErrOutOfRange      = errors.New("polygon: coordinate out of range |X|,|Y| <= 1e4")
	ErrDuplicateVertex = errors.New("polygon: duplicate vertex")
	ErrSelfIntersect   = errors.New("polygon: self-intersecting")
	ErrNotCCW          = errors.New("polygon: not counter-clockwise")
	ErrSelfCheck       = errors.New("polygon: self-check failed")
)

const coordBound = 10000

// Polygon 是校验通过的简单逆时针多边形，构造后只读，可并发使用。
type Polygon struct {
	verts []Point
}

// New 校验：顶点数、坐标范围、无重复、无自交、逆时针；任一失败整体拒绝。
func New(verts []Point) (*Polygon, error) {
	if len(verts) < 3 {
		return nil, ErrTooFewVertices
	}
	seen := make(map[Point]struct{}, len(verts))
	for _, p := range verts {
		if p.X < -coordBound || p.X > coordBound || p.Y < -coordBound || p.Y > coordBound {
			return nil, fmt.Errorf("%w: (%d,%d)", ErrOutOfRange, p.X, p.Y)
		}
		if _, ok := seen[p]; ok {
			return nil, fmt.Errorf("%w: (%d,%d)", ErrDuplicateVertex, p.X, p.Y)
		}
		seen[p] = struct{}{}
	}
	if !poly.Simple(verts) {
		return nil, ErrSelfIntersect
	}
	if !poly.CCW(verts) {
		return nil, ErrNotCCW
	}
	cp := make([]Point, len(verts))
	copy(cp, verts)
	return &Polygon{verts: cp}, nil
}

// Area 返回面积有理数（恒正）。
func (p *Polygon) Area() (Rat, error) { return meas.Area(p.verts), nil }

// Centroid 返回重心有理数点。
func (p *Polygon) Centroid() (PointRat, error) { return meas.Centroid(p.verts), nil }

// triangulatedCentroid 独立代码路径：原点扇形三角剖分，逐三角形有理数加权平均。
func triangulatedCentroid(v []Point) PointRat {
	zero := meas.NewRat(0, 1)
	numX, numY, den := zero, zero, zero
	for i := range v {
		a, b := v[i], v[(i+1)%len(v)]
		w := meas.NewRat(poly.Cross(a, b), 2) // 有向三角形面积
		numX = meas.Add(numX, meas.Mul(w, meas.NewRat(a.X+b.X, 3)))
		numY = meas.Add(numY, meas.Mul(w, meas.NewRat(a.Y+b.Y, 3)))
		den = meas.Add(den, w)
	}
	return PointRat{
		X: meas.NewRat(numX.Num*den.Den, numX.Den*den.Num),
		Y: meas.NewRat(numY.Num*den.Den, numY.Den*den.Num),
	}
}

// SelfCheck 对内置多边形核验四条不变量，全部通过返回 nil。
func (p *Polygon) SelfCheck() error {
	cases := [][]Point{
		{{X: 0, Y: 0}, {X: 6, Y: 0}, {X: 6, Y: 2}, {X: 2, Y: 2}, {X: 2, Y: 6}, {X: 0, Y: 6}}, // L 凹多边形
		{{X: 0, Y: 0}, {X: 4, Y: 0}, {X: 0, Y: 3}},                                           // 三角形
		{{X: 1, Y: 1}, {X: 5, Y: 1}, {X: 5, Y: 5}, {X: 1, Y: 5}},                             // 正方形
	}
	for _, v := range cases {
		pg, err := New(v)
		if err != nil {
			return fmt.Errorf("%w: build: %v", ErrSelfCheck, err)
		}
		cen, _ := pg.Centroid()
		tri := triangulatedCentroid(v)
		if !meas.Eq(cen.X, tri.X) || !meas.Eq(cen.Y, tri.Y) {
			return fmt.Errorf("%w: triangulation mismatch", ErrSelfCheck)
		}
		area, _ := pg.Area()
		if area.Num <= 0 || !meas.Eq(area, meas.NewRat(poly.SignedArea2(v), 2)) {
			return fmt.Errorf("%w: area not positive/true", ErrSelfCheck)
		}
		// 平移不变
		const tx, ty = 7, -3
		mv := make([]Point, len(v))
		for i, q := range v {
			mv[i] = Point{X: q.X + tx, Y: q.Y + ty}
		}
		mp, err := New(mv)
		if err != nil {
			return fmt.Errorf("%w: moved build: %v", ErrSelfCheck, err)
		}
		mc, _ := mp.Centroid()
		if !meas.Eq(mc.X, meas.Add(cen.X, meas.NewRat(tx, 1))) ||
			!meas.Eq(mc.Y, meas.Add(cen.Y, meas.NewRat(ty, 1))) {
			return fmt.Errorf("%w: translation invariance", ErrSelfCheck)
		}
	}
	// 失败不留痕：被拒输入不得产生部分结果
	bad := [][]Point{
		{{X: 0, Y: 0}, {X: 1, Y: 1}},                             // 顶点不足
		{{X: 0, Y: 0}, {X: 4, Y: 4}, {X: 4, Y: 0}, {X: 0, Y: 4}}, // 自交
		{{X: 0, Y: 0}, {X: 0, Y: 6}, {X: 6, Y: 0}},               // 顺时针
		{{X: 0, Y: 0}, {X: 20000, Y: 0}, {X: 0, Y: 1}},           // 坐标越界
		{{X: 0, Y: 0}, {X: 2, Y: 0}, {X: 2, Y: 0}, {X: 0, Y: 2}}, // 顶点重复
	}
	for _, v := range bad {
		if pg, err := New(v); err == nil || pg != nil {
			return fmt.Errorf("%w: rejected input left partial result", ErrSelfCheck)
		}
	}
	return nil
}
