// Package cal 提供凸多边形几何谓词：有向面积、平方距离、对跖顶点。
package cal

// Point 为整数坐标点，合法输入要求 |X|,|Y| <= 1e4（由 api 包校验）。
type Point struct{ X, Y int64 }

// Orient 返回 (b-a)×(c-a)，>0 表示 c 在有向边 a→b 左侧。
func Orient(a, b, c Point) int64 {
	return (b.X-a.X)*(c.Y-a.Y) - (b.Y-a.Y)*(c.X-a.X)
}

// Area2 返回有向二倍面积，与 Orient 同值；其绝对值即点到边所在直线距离的度量。
func Area2(a, b, c Point) int64 { return Orient(a, b, c) }

// Dist2 返回平方距离 (Δx)²+(Δy)²，整数精确。
func Dist2(a, b Point) int64 {
	dx, dy := a.X-b.X, a.Y-b.Y
	return dx*dx + dy*dy
}

// Antipodal 返回边 (poly[i]→poly[(i+1)%n]) 的对跖顶点下标：
// 使 |Area2| 最大的顶点；并列时取下标最小者。
func Antipodal(poly []Point, i int) int {
	n := len(poly)
	a, b := poly[i], poly[(i+1)%n]
	best, bestArea := -1, int64(-1)
	for j := 0; j < n; j++ {
		ar := Area2(a, b, poly[j])
		if ar < 0 {
			ar = -ar
		}
		if ar > bestArea {
			bestArea, best = ar, j
		}
	}
	return best
}
