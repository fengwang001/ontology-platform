// Package cvex 提供凸多边形的基础谓词与单次半平面裁剪，不依赖其他包。
package cvex

// Point 为平面点；坐标用 float64 以容纳裁剪产生的分数交点。
type Point struct{ X, Y float64 }

// Orient 返回 (b-a)×(c-a)，>0 表示 c 在有向边 a→b 的左侧。
func Orient(a, b, c Point) float64 {
	return (b.X-a.X)*(c.Y-a.Y) - (b.Y-a.Y)*(c.X-a.X)
}

// PointLeft 报告 p 是否在有向边 a→b 的左侧（含边界，即 Orient >= 0）。
func PointLeft(a, b, p Point) bool { return Orient(a, b, p) >= 0 }

// ClipLeft 用有向边 a→b 的左半平面裁剪凸多边形 poly（Sutherland–Hodgman），
// 返回保留部分的顶点序列（逆时针），被裁空时返回空切片。
func ClipLeft(poly []Point, a, b Point) []Point {
	out := make([]Point, 0, len(poly)+1)
	for i, s := range poly {
		e := poly[(i+1)%len(poly)]
		sIn, eIn := PointLeft(a, b, s), PointLeft(a, b, e)
		switch {
		case sIn && eIn:
			out = append(out, e)
		case sIn && !eIn:
			out = append(out, intersect(s, e, a, b))
		case !sIn && eIn:
			out = append(out, intersect(s, e, a, b), e)
		}
	}
	return dedup(out)
}

// intersect 求线段 s→e 与直线 a→b 的交点；调用时恰有一端在左半平面内。
// 当 s（或 e）恰在直线上时 t=0（或 1），结果精确等于 s（或 e）。
func intersect(s, e, a, b Point) Point {
	os, oe := Orient(a, b, s), Orient(a, b, e)
	t := os / (os - oe)
	return Point{s.X + t*(e.X-s.X), s.Y + t*(e.Y-s.Y)}
}

// dedup 去掉相邻重复点（含首尾相接处）。
func dedup(p []Point) []Point {
	out := p[:0]
	for _, v := range p {
		if len(out) == 0 || out[len(out)-1] != v {
			out = append(out, v)
		}
	}
	for len(out) > 1 && out[0] == out[len(out)-1] {
		out = out[:len(out)-1]
	}
	return out
}

// Area2 返回多边形的两倍有向面积（逆时针为正）。
func Area2(p []Point) float64 {
	var s float64
	for i := range p {
		j := (i + 1) % len(p)
		s += p[i].X*p[j].Y - p[j].X*p[i].Y
	}
	return s
}
