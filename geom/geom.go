package geom

import "math"

// Point 是平面上的点，ID 由调用方保证唯一。
type Point struct {
	ID uint64
	X  float64
	Y  float64
}

// Rect 是左闭右开矩形 [X0,X1) × [Y0,Y1)。
type Rect struct {
	X0, Y0, X1, Y1 float64
}

// Valid 报告坐标是否可被索引：拒绝 NaN 与 Inf。+0/-0 经 == 判定天然相等。
func Valid(x, y float64) bool {
	return !math.IsNaN(x) && !math.IsNaN(y) && !math.IsInf(x, 0) && !math.IsInf(y, 0)
}

// Contains 报告点 p 是否落在 r 内（左闭右开，边界唯一归属）。
func (r Rect) Contains(p Point) bool {
	return p.X >= r.X0 && p.X < r.X1 && p.Y >= r.Y0 && p.Y < r.Y1
}

// Intersects 报告两矩形是否相交（半开区间，仅共边不算相交）。
func (r Rect) Intersects(o Rect) bool {
	return r.X0 < o.X1 && o.X0 < r.X1 && r.Y0 < o.Y1 && o.Y0 < r.Y1
}

// ContainsRect 报告 o 是否完全落在 r 内（含边界重合）。
func (r Rect) ContainsRect(o Rect) bool {
	return o.X0 >= r.X0 && o.X1 <= r.X1 && o.Y0 >= r.Y0 && o.Y1 <= r.Y1
}

// Empty 报告矩形是否退化为线或点（半开语义下无内点）。
func (r Rect) Empty() bool { return r.X0 >= r.X1 || r.Y0 >= r.Y1 }
