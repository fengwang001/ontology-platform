// Package poly 提供简单多边形的谓词：叉积、方向判定、无自交判定、逆时针判定。
// 不依赖其他包。
package poly

// Point 是整数坐标点。
type Point struct {
	X, Y int64
}

// Cross 返回向量叉积 a×b = a.X*b.Y − b.X*a.Y。
func Cross(a, b Point) int64 { return a.X*b.Y - b.X*a.Y }

// Orient 返回 (b−a)×(c−a)：>0 表示 c 在 ab 左侧，<0 右侧，=0 共线。
func Orient(a, b, c Point) int64 {
	return Cross(Point{b.X - a.X, b.Y - a.Y}, Point{c.X - a.X, c.Y - a.Y})
}

// onSeg 报告共线的 q 是否落在闭线段 pr 上（调用前须保证共线）。
func onSeg(p, q, r Point) bool {
	return q.X >= min(p.X, r.X) && q.X <= max(p.X, r.X) &&
		q.Y >= min(p.Y, r.Y) && q.Y <= max(p.Y, r.Y)
}

// segIntersect 报告闭线段 p1p2 与 q1q2 是否相交（含端点接触与共线重叠）。
func segIntersect(p1, p2, q1, q2 Point) bool {
	d1 := Orient(q1, q2, p1)
	d2 := Orient(q1, q2, p2)
	d3 := Orient(p1, p2, q1)
	d4 := Orient(p1, p2, q2)
	if ((d1 > 0 && d2 < 0) || (d1 < 0 && d2 > 0)) &&
		((d3 > 0 && d4 < 0) || (d3 < 0 && d4 > 0)) {
		return true
	}
	if d1 == 0 && onSeg(q1, p1, q2) {
		return true
	}
	if d2 == 0 && onSeg(q1, p2, q2) {
		return true
	}
	if d3 == 0 && onSeg(p1, q1, p2) {
		return true
	}
	if d4 == 0 && onSeg(p1, q2, p2) {
		return true
	}
	return false
}

// Simple 报告顶点序列是否构成无自交多边形（含顶点接触式自交）。
// 只检查非相邻边对；相邻边共享端点属正常。
func Simple(verts []Point) bool {
	n := len(verts)
	if n < 3 {
		return false
	}
	for i := 0; i < n; i++ {
		a1, a2 := verts[i], verts[(i+1)%n]
		for j := i + 1; j < n; j++ {
			if j == i+1 || (i == 0 && j == n-1) {
				continue // 相邻边
			}
			if segIntersect(a1, a2, verts[j], verts[(j+1)%n]) {
				return false
			}
		}
	}
	return true
}

// SignedArea2 返回鞋带和 Σ(x_i·y_{i+1} − x_{i+1}·y_i)，逆时针为正。
func SignedArea2(verts []Point) int64 {
	var s int64
	for i := range verts {
		s += Cross(verts[i], verts[(i+1)%len(verts)])
	}
	return s
}

// CCW 报告顶点是否严格逆时针（鞋带和 > 0）。
func CCW(verts []Point) bool { return SignedArea2(verts) > 0 }
