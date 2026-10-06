// Package bikefence 实现共享单车电子围栏还车判定与跨围栏调度服务。
package bikefence

import "math/big"

// Point 是整数坐标平面上的点。
type Point struct {
	X, Y int64
}

// FenceKind 表示围栏类别。
type FenceKind int

const (
	// KindOperating 运营区。
	KindOperating FenceKind = iota
	// KindNoParking 禁停区。
	KindNoParking
	// KindReward 奖励区。
	KindReward
)

func (k FenceKind) String() string {
	switch k {
	case KindOperating:
		return "operating"
	case KindNoParking:
		return "no_parking"
	case KindReward:
		return "reward"
	}
	return "unknown"
}

// Fence 是一条已登记的围栏。
type Fence struct {
	ID       string
	Kind     FenceKind
	Vertices []Point
	Capacity int

	bbox box
}

func (p Point) sub(q Point) Point { return Point{X: p.X - q.X, Y: p.Y - q.Y} }

// cross 用任意精度整数计算向量叉积，杜绝溢出导致的边界误判。
func cross(a, b Point) *big.Int {
	ax := big.NewInt(a.X)
	ay := big.NewInt(a.Y)
	ax.Mul(ax, big.NewInt(b.Y))
	ay.Mul(ay, big.NewInt(b.X))
	return ax.Sub(ax, ay)
}

func crossAt(a, b, c Point) *big.Int {
	return cross(b.sub(a), c.sub(a))
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// onSegment 判断 p 是否落在闭线段 ab 上（含端点）。
func onSegment(a, b, p Point) bool {
	if crossAt(a, b, p).Sign() != 0 {
		return false
	}
	return p.X >= minInt64(a.X, b.X) && p.X <= maxInt64(a.X, b.X) &&
		p.Y >= minInt64(a.Y, b.Y) && p.Y <= maxInt64(a.Y, b.Y)
}

// pointInPolygon 按“边界点视为内部”判定点是否属于简单多边形（含顶点与边）。
// 射线法处理严格内部，逐边检查处理边界。
func pointInPolygon(vs []Point, p Point) bool {
	n := len(vs)
	for i := 0; i < n; i++ {
		if onSegment(vs[i], vs[(i+1)%n], p) {
			return true
		}
	}
	inside := false
	for i, j := 0, n-1; i < n; j, i = i, i+1 {
		a, b := vs[i], vs[j]
		if (a.Y > p.Y) != (b.Y > p.Y) {
			// 比较 x 交点与 p.X，交叉相乘避免浮点误差。
			lhs := big.NewInt(p.Y - a.Y)
			lhs.Mul(lhs, big.NewInt(b.X-a.X))
			rhs := big.NewInt(p.X - a.X)
			rhs.Mul(rhs, big.NewInt(b.Y-a.Y))
			den := big.NewInt(b.Y - a.Y)
			if (den.Sign() > 0 && lhs.Cmp(rhs) > 0) || (den.Sign() < 0 && lhs.Cmp(rhs) < 0) {
				inside = !inside
			}
		}
	}
	return inside
}

// segmentsIntersect 判断两条闭线段是否相交（含端点相接、共线重叠）。
func segmentsIntersect(a, b, c, d Point) bool {
	if onSegment(a, b, c) || onSegment(a, b, d) || onSegment(c, d, a) || onSegment(c, d, b) {
		return true
	}
	d1 := crossAt(c, d, a)
	d2 := crossAt(c, d, b)
	d3 := crossAt(a, b, c)
	d4 := crossAt(a, b, d)
	return ((d1.Sign() > 0 && d2.Sign() < 0) || (d1.Sign() < 0 && d2.Sign() > 0)) &&
		((d3.Sign() > 0 && d4.Sign() < 0) || (d3.Sign() < 0 && d4.Sign() > 0))
}

// polygonsIntersect 判断两个简单多边形是否相交：边相交，或任一方顶点落在对方内部。
func polygonsIntersect(a, b []Point) bool {
	// 轴对齐矩形（围栏常见形态）走精确 int64 快速路径，避免大规模批量登记时
	// 为两两校验分配 big.Int；一般多边形仍使用下方逐边的精确判定。
	if ra, ok := rectOf(a); ok {
		if rb, ok := rectOf(b); ok {
			return !(ra.maxX < rb.minX || rb.maxX < ra.minX ||
				ra.maxY < rb.minY || rb.maxY < ra.minY)
		}
	}
	na, nb := len(a), len(b)
	for i := 0; i < na; i++ {
		for j := 0; j < nb; j++ {
			if segmentsIntersect(a[i], a[(i+1)%na], b[j], b[(j+1)%nb]) {
				return true
			}
		}
	}
	return pointInPolygon(a, b[0]) || pointInPolygon(b, a[0])
}

// rectOf 在多边形恰为 4 点轴对齐矩形时返回其包围盒，否则 ok=false。
func rectOf(vs []Point) (box, bool) {
	if len(vs) != 4 {
		return box{}, false
	}
	xs := map[int64]int{}
	ys := map[int64]int{}
	for _, p := range vs {
		xs[p.X]++
		ys[p.Y]++
	}
	if len(xs) != 2 || len(ys) != 2 {
		return box{}, false
	}
	for _, n := range xs {
		if n != 2 {
			return box{}, false
		}
	}
	for _, n := range ys {
		if n != 2 {
			return box{}, false
		}
	}
	b := bboxOf(vs)
	// 确认四条边确为该包围盒的边（相邻点 x 或 y 相等）。
	for i := 0; i < 4; i++ {
		a, c := vs[i], vs[(i+1)%4]
		if a.X != c.X && a.Y != c.Y {
			return box{}, false
		}
	}
	return b, true
}

// polygonContains 判断 outer 是否完全包含 inner（边界重合也算包含，故奖励区可紧贴运营区边界）。
func polygonContains(outer, inner []Point) bool {
	if ra, ok := rectOf(outer); ok {
		if rb, ok2 := rectOf(inner); ok2 {
			return rb.minX >= ra.minX && rb.maxX <= ra.maxX &&
				rb.minY >= ra.minY && rb.maxY <= ra.maxY
		}
	}
	for _, p := range inner {
		if !pointInPolygon(outer, p) {
			return false
		}
	}
	no, ni := len(outer), len(inner)
	for i := 0; i < ni; i++ {
		for j := 0; j < no; j++ {
			// 边穿越（非仅端点接触）不属于包含关系。
			if segmentsProperCross(inner[i], inner[(i+1)%ni], outer[j], outer[(j+1)%no]) {
				return false
			}
		}
	}
	return true
}

// segmentsProperCross 判定边的真穿越；端点相接（紧贴）不算穿越。
func segmentsProperCross(a, b, c, d Point) bool {
	d1 := crossAt(c, d, a)
	d2 := crossAt(c, d, b)
	d3 := crossAt(a, b, c)
	d4 := crossAt(a, b, d)
	return ((d1.Sign() > 0 && d2.Sign() < 0) || (d1.Sign() < 0 && d2.Sign() > 0)) &&
		((d3.Sign() > 0 && d4.Sign() < 0) || (d3.Sign() < 0 && d4.Sign() > 0))
}
