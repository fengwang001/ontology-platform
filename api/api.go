// Package api 是对外门面：构造时校验（简单、逆时针、无重复、坐标合法），
// 之后提供精确的面积与重心。依赖 meas；错误一律为可判定的哨兵错误。
package api

import (
	"errors"

	"ontology/meas"
	"ontology/poly"
)

// 可判定哨兵错误，互不相同。
var (
	ErrTooFewVertices  = errors.New("api: 顶点数不足（少于 3 个）")
	ErrOutOfRange      = errors.New("api: 坐标越界（|X|>1e4 或 |Y|>1e4）")
	ErrDuplicateVertex = errors.New("api: 顶点重复（非法输入）")
	ErrSelfIntersect   = errors.New("api: 多边形自交")
	ErrNotCCW          = errors.New("api: 非逆时针")
)

// 对外类型别名。
type (
	Point    = poly.Point
	Rat      = meas.Rat
	PointRat = meas.PointRat
)

// coordLimit 是坐标绝对值上限。
const coordLimit = 10000

// Polygon 是校验通过的多边形，可并发只读使用。
type Polygon struct {
	m *meas.Polygon
}

// New 校验并构造多边形；任何校验失败都整体失败，返回 nil 与哨兵错误，
// 不产生任何部分结果。
func New(verts []Point) (*Polygon, error) {
	if len(verts) < 3 {
		return nil, ErrTooFewVertices
	}
	seen := make(map[Point]struct{}, len(verts))
	for _, p := range verts {
		if p.X < -coordLimit || p.X > coordLimit || p.Y < -coordLimit || p.Y > coordLimit {
			return nil, ErrOutOfRange
		}
		if _, dup := seen[p]; dup {
			return nil, ErrDuplicateVertex
		}
		seen[p] = struct{}{}
	}
	if !poly.Simple(verts) {
		return nil, ErrSelfIntersect
	}
	if !poly.CCW(verts) {
		return nil, ErrNotCCW // 含顺时针与退化（面积为 0）；不得取绝对值纠正
	}
	return &Polygon{m: meas.NewPolygon(verts)}, nil
}

// Area 返回精确鞋带面积（恒正）。
func (p *Polygon) Area() (Rat, error) { return p.m.Area(), nil }

// Centroid 返回精确重心。
func (p *Polygon) Centroid() (PointRat, error) { return p.m.Centroid(), nil }

// fanCentroid 独立实现：从 v0 扇形三角剖分，各三角形以有向面积为权做重心加权平均。
func fanCentroid(v []Point) PointRat {
	var sx, sy, sw int64
	v0 := v[0]
	for i := 1; i+1 < len(v); i++ {
		a, b := v[i], v[i+1]
		w := poly.Cross(poly.Point{X: a.X - v0.X, Y: a.Y - v0.Y}, poly.Point{X: b.X - v0.X, Y: b.Y - v0.Y})
		sw += w
		sx += w * (v0.X + a.X + b.X)
		sy += w * (v0.Y + a.Y + b.Y)
	}
	return PointRat{X: meas.NewRat(sx, 3*sw), Y: meas.NewRat(sy, 3*sw)}
}

// SelfCheck 对一组内置多边形核验四条不变量，全部通过返回 nil。
func SelfCheck() error {
	sets := [][]Point{
		{{X: 0, Y: 0}, {X: 6, Y: 0}, {X: 6, Y: 2}, {X: 2, Y: 2}, {X: 2, Y: 6}, {X: 0, Y: 6}}, // L 形
		{{X: 0, Y: 0}, {X: 4, Y: 1}, {X: 3, Y: 5}, {X: -1, Y: 4}},                            // 斜四边形
		{{X: -3, Y: -2}, {X: 5, Y: -1}, {X: 1, Y: 4}},                                        // 三角形
	}
	for _, v := range sets {
		pg, err := New(v)
		if err != nil {
			return err
		}
		a, _ := pg.Area()
		c, _ := pg.Centroid()
		if a.Num <= 0 || a != meas.NewRat(poly.SignedArea2(v), 2) { // 不变量 3
			return errors.New("api: 自检失败：面积非正或不真")
		}
		if c != fanCentroid(v) { // 不变量 1
			return errors.New("api: 自检失败：与三角剖分不一致")
		}
		sh := make([]Point, len(v)) // 不变量 2
		for i, p := range v {
			sh[i] = Point{X: p.X + 7, Y: p.Y - 3}
		}
		pg2, err := New(sh)
		if err != nil {
			return err
		}
		c2, _ := pg2.Centroid()
		if c2.X != meas.NewRat(c.X.Num+7*c.X.Den, c.X.Den) ||
			c2.Y != meas.NewRat(c.Y.Num-3*c.Y.Den, c.Y.Den) {
			return errors.New("api: 自检失败：平移不变被破坏")
		}
	}
	// 不变量 4：被拒输入不留痕，且已有对象不受影响。
	bad := [][]Point{
		{{X: 0, Y: 0}, {X: 4, Y: 4}, {X: 4, Y: 0}, {X: 0, Y: 4}}, // 自交
		{{X: 0, Y: 0}, {X: 0, Y: 2}, {X: 2, Y: 0}},               // 顺时针
		{{X: 0, Y: 0}, {X: 1, Y: 1}},                             // 顶点不足
		{{X: 0, Y: 0}, {X: 20000, Y: 0}, {X: 0, Y: 3}},           // 越界
		{{X: 0, Y: 0}, {X: 2, Y: 0}, {X: 2, Y: 2}, {X: 0, Y: 0}}, // 重复
	}
	for _, v := range bad {
		if pg, err := New(v); err == nil || pg != nil {
			return errors.New("api: 自检失败：拒绝产生了部分结果")
		}
	}
	return nil
}
