// Package geom 定义平面点与左闭右开矩形及其包含、相交判定。
package geom

import "math"

// Point 是平面上的点。
type Point struct {
	X, Y float64
}

// Valid 报告坐标是否可入索引：拒绝 NaN 与 ±Inf。
func (p Point) Valid() bool {
	return !math.IsNaN(p.X) && !math.IsNaN(p.Y) &&
		!math.IsInf(p.X, 0) && !math.IsInf(p.Y, 0)
}

// Rect 是左闭右开的半开矩形 [X0,X1) × [Y0,Y1)。
type Rect struct {
	X0, Y0, X1, Y1 float64
}

// Empty 报告矩形是否退化为空集（X0>=X1 或 Y0>=Y1）。
func (r Rect) Empty() bool {
	return r.X0 >= r.X1 || r.Y0 >= r.Y1
}

// Contains 报告点 p 是否落在矩形内（左闭右开）。
func (r Rect) Contains(p Point) bool {
	return p.X >= r.X0 && p.X < r.X1 && p.Y >= r.Y0 && p.Y < r.Y1
}

// Intersects 报告两个半开矩形是否有公共点。
// 退化矩形（X0==X1 或 Y0==Y1）为空，与一切矩形相离。
func (r Rect) Intersects(o Rect) bool {
	if r.Empty() || o.Empty() {
		return false
	}
	return r.X0 < o.X1 && o.X0 < r.X1 && r.Y0 < o.Y1 && o.Y0 < r.Y1
}

// ContainsRect 报告 o 是否完整落在 r 内（用于查询整取剪枝）。
func (r Rect) ContainsRect(o Rect) bool {
	if o.Empty() {
		return false
	}
	return r.X0 <= o.X0 && o.X1 <= r.X1 && r.Y0 <= o.Y0 && o.Y1 <= r.Y1
}

// Mid 返回矩形中心，即分裂线交点。
func (r Rect) Mid() Point {
	return Point{(r.X0 + r.X1) / 2, (r.Y0 + r.Y1) / 2}
}

// Splittable 报告矩形是否还能在浮点精度内再分。
func (r Rect) Splittable() bool {
	m := r.Mid()
	return m.X > r.X0 && m.X < r.X1 && m.Y > r.Y0 && m.Y < r.Y1
}
