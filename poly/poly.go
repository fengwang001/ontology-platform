// Package poly 提供简单多边形的几何谓词：叉积、定向、无自交判定、逆时针判定。
// 不依赖其他任何包。
package poly

// Point 是整数坐标点。
type Point struct {
	X, Y int64
}

// Cross 返回二维叉积 a×b = a.X*b.Y − a.Y*b.X。
func Cross(a, b Point) int64 { return a.X*b.Y - a.Y*b.X }

// Orient 返回有序三点 (a,b,c) 的定向：>0 逆时针，<0 顺时针，0 共线。
func Orient(a, b, c Point) int {
	v := Cross(Point{b.X - a.X, b.Y - a.Y}, Point{c.X - a.X, c.Y - a.Y})
	switch {
	case v > 0:
		return 1
	case v < 0:
		return -1
	default:
		return 0
	}
}

// onSeg 报告共线点 q 是否落在闭线段 [a,b] 上（调用前须已知共线）。
func onSeg(a, b, q Point) bool {
	return min(a.X, b.X) <= q.X && q.X <= max(a.X, b.X) &&
		min(a.Y, b.Y) <= q.Y && q.Y <= max(a.Y, b.Y)
}

// segHit 报告闭线段 p1p2 与 p3p4 是否有任何公共点（含端点接触与共线重叠）。
func segHit(p1, p2, p3, p4 Point) bool {
	d1 := Orient(p3, p4, p1)
	d2 := Orient(p3, p4, p2)
	d3 := Orient(p1, p2, p3)
	d4 := Orient(p1, p2, p4)
	if ((d1 > 0 && d2 < 0) || (d1 < 0 && d2 > 0)) &&
		((d3 > 0 && d4 < 0) || (d3 < 0 && d4 > 0)) {
		return true
	}
	if d1 == 0 && onSeg(p3, p4, p1) {
		return true
	}
	if d2 == 0 && onSeg(p3, p4, p2) {
		return true
	}
	if d3 == 0 && onSeg(p1, p2, p3) {
		return true
	}
	return d4 == 0 && onSeg(p1, p2, p4)
}

// Simple 报告顶点序列是否构成无自交多边形：任意两条非相邻边无公共点。
// 顶点数 < 3 时返回 false。
func Simple(verts []Point) bool {
	n := len(verts)
	if n < 3 {
		return false
	}
	for i := 0; i < n; i++ {
		a1, a2 := verts[i], verts[(i+1)%n]
		for j := i + 1; j < n; j++ {
			if j == i+1 || (i == 0 && j == n-1) {
				continue // 相邻边共享顶点，跳过
			}
			if segHit(a1, a2, verts[j], verts[(j+1)%n]) {
				return false
			}
		}
	}
	return true
}

// SignedArea2 返回二倍有向面积 Σ(xᵢyᵢ₊₁ − xᵢ₊₁yᵢ)，逆时针为正。
func SignedArea2(verts []Point) int64 {
	var s int64
	for i := range verts {
		s += Cross(verts[i], verts[(i+1)%len(verts)])
	}
	return s
}

// CCW 报告顶点序列是否严格逆时针（二倍有向面积 > 0）。
func CCW(verts []Point) bool { return SignedArea2(verts) > 0 }
