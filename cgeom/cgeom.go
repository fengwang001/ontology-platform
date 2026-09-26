// Package cgeom 提供圆-多边形关系判定所需的几何谓词。
// 不依赖工程内其他包；全程整数/有理数精确运算，不引入浮点。
package cgeom

import "math/big"

// Point 整数坐标点（坐标范围由上层校验：|X|,|Y| ≤ 1e4）。
type Point struct{ X, Y int }

// Rat 非负有理数 N/D（D>0），表示精确的平方距离，已约分。
type Rat struct{ N, D int64 }

func gcd(a, b int64) int64 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// norm 约分构造 Rat。
func norm(n, d int64) Rat {
	g := gcd(n, d)
	return Rat{n / g, d / g}
}

// Cmp 比较两个有理数：r<s 返回 -1，相等返回 0，r>s 返回 1。
// 用 big.Int 交叉相乘，避免 int64 溢出。
func (r Rat) Cmp(s Rat) int {
	l := new(big.Int).Mul(big.NewInt(r.N), big.NewInt(s.D))
	rt := new(big.Int).Mul(big.NewInt(s.N), big.NewInt(r.D))
	return l.Cmp(rt)
}

// PointSegDist2 点 p 到线段 ab 的精确平方距离：
// 把 p 投影到 ab 所在直线，投影参数截断到 [0,1]，再算欧氏距离的平方。
// 内部投影时平方距离是有理数，以 Rat 返回保持精确。
func PointSegDist2(p, a, b Point) Rat {
	dx := int64(b.X - a.X)
	dy := int64(b.Y - a.Y)
	px := int64(p.X - a.X)
	py := int64(p.Y - a.Y)
	den := dx*dx + dy*dy // |ab|²，也是投影参数 t 的分母
	if den == 0 {
		return Rat{px*px + py*py, 1}
	}
	t := px*dx + py*dy
	switch {
	case t <= 0: // 截断到端点 a
		return Rat{px*px + py*py, 1}
	case t >= den: // 截断到端点 b
		qx := int64(p.X - b.X)
		qy := int64(p.Y - b.Y)
		return Rat{qx*qx + qy*qy, 1}
	default: // 投影落在线段内部：d² = |ap|² - t²/|ab|²
		return norm((px*px+py*py)*den-t*t, den)
	}
}

// OnSeg 判断 p 是否在线段 ab 上（含端点）。
func OnSeg(p, a, b Point) bool {
	dx := int64(b.X - a.X)
	dy := int64(b.Y - a.Y)
	px := int64(p.X - a.X)
	py := int64(p.Y - a.Y)
	if dx*py != dy*px {
		return false
	}
	return min(a.X, b.X) <= p.X && p.X <= max(a.X, b.X) &&
		min(a.Y, b.Y) <= p.Y && p.Y <= max(a.Y, b.Y)
}

// PointInPoly 射线法判断 p 是否在简单多边形内，边界上的点算在内。
func PointInPoly(p Point, poly []Point) bool {
	inside := false
	n := len(poly)
	for i := 0; i < n; i++ {
		a, b := poly[i], poly[(i+1)%n]
		if OnSeg(p, a, b) {
			return true
		}
		if (a.Y > p.Y) != (b.Y > p.Y) {
			lo, hi := a, b
			if lo.Y > hi.Y {
				lo, hi = hi, lo
			}
			// 交点横坐标 x = lo.X + (p.Y-lo.Y)*(hi.X-lo.X)/dy，dy>0；
			// x > p.X 等价于下式（交叉相乘避免除法）。
			dy := int64(hi.Y - lo.Y)
			if int64(p.Y-lo.Y)*int64(hi.X-lo.X) > int64(p.X-lo.X)*dy {
				inside = !inside
			}
		}
	}
	return inside
}
