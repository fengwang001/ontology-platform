// Package cint 用逐边半平面裁剪计算两个凸多边形的交集，依赖 cvex。
package cint

import "ontology/cvex"

// minArea2 为两倍面积的下限，低于它视为面积为零的退化交集。
const minArea2 = 1e-9

// Clipper 计算凸多边形交集。
// clips 是非导出计数器，记录最近一次 Intersect 实际执行的半平面裁剪次数，
// 不出现在任何公开接口中。
type Clipper struct{ clips int }

// Intersect 返回 a∩b 的顶点序列（逆时针）；交集为空或面积为零时返回 nil。
// 包围盒不相交时 O(1) 判空，不做任何逐边裁剪。
func (c *Clipper) Intersect(a, b []cvex.Point) []cvex.Point {
	c.clips = 0
	if !bboxOverlap(a, b) {
		return nil
	}
	out := a
	for i := range b {
		out = cvex.ClipLeft(out, b[i], b[(i+1)%len(b)])
		c.clips++
		if len(out) == 0 {
			break
		}
	}
	if len(out) < 3 || cvex.Area2(out) <= minArea2 {
		return nil
	}
	return out
}

// bboxOverlap 报告两个多边形的包围盒是否相交（含边界相触）。
func bboxOverlap(a, b []cvex.Point) bool {
	aMin, aMax := bbox(a)
	bMin, bMax := bbox(b)
	return aMin.X <= bMax.X && bMin.X <= aMax.X &&
		aMin.Y <= bMax.Y && bMin.Y <= aMax.Y
}

func bbox(p []cvex.Point) (min, max cvex.Point) {
	min, max = p[0], p[0]
	for _, v := range p[1:] {
		if v.X < min.X {
			min.X = v.X
		}
		if v.X > max.X {
			max.X = v.X
		}
		if v.Y < min.Y {
			min.Y = v.Y
		}
		if v.Y > max.Y {
			max.Y = v.Y
		}
	}
	return min, max
}
