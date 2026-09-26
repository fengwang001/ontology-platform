// Package cal 提供凸多边形直径计算所需的几何谓词，不依赖其他包。
package cal

// Point 是整数坐标点，合法输入要求 |X|,|Y| <= 10^4（由上层校验）。
type Point struct{ X, Y int64 }

// Area2 返回有向二倍面积 (b-a)×(c-a)：>0 表示 c 在有向边 a→b 左侧。
func Area2(a, b, c Point) int64 {
	return (b.X-a.X)*(c.Y-a.Y) - (b.Y-a.Y)*(c.X-a.X)
}

// Orient 返回 c 相对有向边 a→b 的朝向：+1 左侧，-1 右侧，0 共线。
func Orient(a, b, c Point) int {
	switch v := Area2(a, b, c); {
	case v > 0:
		return 1
	case v < 0:
		return -1
	default:
		return 0
	}
}

// Dist2 返回平方距离，整数精确。
func Dist2(a, b Point) int64 {
	dx, dy := a.X-b.X, a.Y-b.Y
	return dx*dx + dy*dy
}

// Antipodal 返回边 (poly[i] → poly[(i+1)%n]) 的对跖顶点下标：
// 即到该边所在直线距离（有向面积绝对值）最大的顶点，线性扫描，取首个最大者。
func Antipodal(poly []Point, i int) int {
	n := len(poly)
	a, b := poly[i], poly[(i+1)%n]
	best, bestArea := i, int64(-1)
	for j := 0; j < n; j++ {
		if v := abs(Area2(a, b, poly[j])); v > bestArea {
			best, bestArea = j, v
		}
	}
	return best
}

func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
