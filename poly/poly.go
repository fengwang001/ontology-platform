// Package poly 提供简单多边形的几何谓词：定向、点在多边形内、线段求交。
// 不依赖其他包。输入顶点为整数坐标（|X|,|Y|≤1e4 时叉积在 float64 下精确），
// 边交点可为有理坐标，故 Point 用 float64。
package poly

// Point 为平面点。
type Point struct{ X, Y float64 }

// Orient 返回 (a,b,c) 的定向符号：+1 逆时针，-1 顺时针，0 共线。
func Orient(a, b, c Point) int {
	cr := (b.X-a.X)*(c.Y-a.Y) - (b.Y-a.Y)*(c.X-a.X)
	switch {
	case cr > 0:
		return 1
	case cr < 0:
		return -1
	}
	return 0
}

// OnSeg 判定 p 是否在线段 ab 上（含端点）。
func OnSeg(a, b, p Point) bool {
	return Orient(a, b, p) == 0 &&
		p.X >= min(a.X, b.X) && p.X <= max(a.X, b.X) &&
		p.Y >= min(a.Y, b.Y) && p.Y <= max(a.Y, b.Y)
}

// PointInPoly 射线法判定 p 是否在多边形严格内部；边界上的点不算内。
func PointInPoly(p Point, poly []Point) bool {
	inside := false
	n := len(poly)
	for i := 0; i < n; i++ {
		a, b := poly[i], poly[(i+1)%n]
		if OnSeg(a, b, p) {
			return false // 边界不算内
		}
		if (a.Y > p.Y) != (b.Y > p.Y) {
			if x := a.X + (p.Y-a.Y)*(b.X-a.X)/(b.Y-a.Y); x > p.X {
				inside = !inside
			}
		}
	}
	return inside
}

// SegIntersect 求线段 ab 与 cd 的交点：仅当两线段在各自严格内部相交
// 时返回交点与 true；端点相接、共线、平行均返回 false。
func SegIntersect(a, b, c, d Point) (Point, bool) {
	o1, o2 := Orient(a, b, c), Orient(a, b, d)
	o3, o4 := Orient(c, d, a), Orient(c, d, b)
	if o1*o2 >= 0 || o3*o4 >= 0 {
		return Point{}, false
	}
	dx, dy := b.X-a.X, b.Y-a.Y
	t := ((c.X-a.X)*(d.Y-c.Y) - (c.Y-a.Y)*(d.X-c.X)) / (dx*(d.Y-c.Y) - dy*(d.X-c.X))
	return Point{a.X + t*dx, a.Y + t*dy}, true
}

// SegCross 判定两线段是否有任何公共点（含端点相接与共线重叠）。
func SegCross(a, b, c, d Point) bool {
	o1, o2 := Orient(a, b, c), Orient(a, b, d)
	o3, o4 := Orient(c, d, a), Orient(c, d, b)
	if o1*o2 < 0 && o3*o4 < 0 {
		return true
	}
	return o1 == 0 && OnSeg(a, b, c) || o2 == 0 && OnSeg(a, b, d) ||
		o3 == 0 && OnSeg(c, d, a) || o4 == 0 && OnSeg(c, d, b)
}

// Area2 返回多边形有向面积的两倍（逆时针为正），鞋带公式。
func Area2(p []Point) float64 {
	s := 0.0
	for i := range p {
		a, b := p[i], p[(i+1)%len(p)]
		s += a.X*b.Y - a.Y*b.X
	}
	return s
}
