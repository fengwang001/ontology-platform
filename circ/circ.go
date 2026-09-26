// Package circ 由边界点确定圆，全部用精确有理数，不依赖其他包。
package circ

import "math/big"

// Point 是整数坐标二维点。
type Point struct{ X, Y int }

// Circle 用有理数圆心与半径平方精确表示一个圆，B 为确定该圆的边界点。
type Circle struct {
	Cx, Cy *big.Rat
	R2     *big.Rat
	B      []Point
}

func rat(v int64) *big.Rat { return big.NewRat(v, 1) }

// Dist2 返回两点距离的平方。
func Dist2(a, b Point) int64 {
	dx, dy := int64(a.X-b.X), int64(a.Y-b.Y)
	return dx*dx + dy*dy
}

// Orient 返回三点转向：正为逆时针，负为顺时针，0 为共线。
func Orient(a, b, c Point) int {
	v := int64(b.X-a.X)*int64(c.Y-a.Y) - int64(b.Y-a.Y)*int64(c.X-a.X)
	switch {
	case v > 0:
		return 1
	case v < 0:
		return -1
	}
	return 0
}

// FromPoint 返回以 p 为圆心、半径 0 的圆。
func FromPoint(p Point) Circle {
	return Circle{rat(int64(p.X)), rat(int64(p.Y)), rat(0), []Point{p}}
}

// FromDiameter 返回以 a、b 为直径的圆。
func FromDiameter(a, b Point) Circle {
	cx := new(big.Rat).Add(rat(int64(a.X)), rat(int64(b.X)))
	cy := new(big.Rat).Add(rat(int64(a.Y)), rat(int64(b.Y)))
	two := rat(2)
	cx.Quo(cx, two)
	cy.Quo(cy, two)
	r2 := new(big.Rat).Quo(rat(Dist2(a, b)), rat(4))
	return Circle{cx, cy, r2, []Point{a, b}}
}

// dot 返回向量 (p-v)·(q-v)。
func dot(v, p, q Point) int64 {
	return int64(p.X-v.X)*int64(q.X-v.X) + int64(p.Y-v.Y)*int64(q.Y-v.Y)
}

// FromThree 返回三点的最小包围圆：共线退化为两端点直径圆，
// 直角/钝角回退为最长边直径圆，否则为外接圆。
func FromThree(a, b, c Point) Circle {
	if Orient(a, b, c) == 0 {
		return farthestDiameter(a, b, c)
	}
	if dot(a, b, c) <= 0 {
		return FromDiameter(b, c)
	}
	if dot(b, a, c) <= 0 {
		return FromDiameter(a, c)
	}
	if dot(c, a, b) <= 0 {
		return FromDiameter(a, b)
	}
	return circumcircle(a, b, c)
}

// farthestDiameter 返回三点中距离最远两点的直径圆。
func farthestDiameter(a, b, c Point) Circle {
	best, d := [2]Point{a, b}, Dist2(a, b)
	if d2 := Dist2(a, c); d2 > d {
		best, d = [2]Point{a, c}, d2
	}
	if d2 := Dist2(b, c); d2 > d {
		best = [2]Point{b, c}
	}
	return FromDiameter(best[0], best[1])
}

// circumcircle 求不共线三点的外接圆。
func circumcircle(a, b, c Point) Circle {
	a2 := int64(a.X*a.X + a.Y*a.Y)
	b2 := int64(b.X*b.X + b.Y*b.Y)
	c2 := int64(c.X*c.X + c.Y*c.Y)
	d := 2 * (int64(a.X)*int64(b.Y-c.Y) + int64(b.X)*int64(c.Y-a.Y) + int64(c.X)*int64(a.Y-b.Y))
	ux := a2*int64(b.Y-c.Y) + b2*int64(c.Y-a.Y) + c2*int64(a.Y-b.Y)
	uy := a2*int64(c.X-b.X) + b2*int64(a.X-c.X) + c2*int64(b.X-a.X)
	cx := new(big.Rat).Quo(rat(ux), rat(d))
	cy := new(big.Rat).Quo(rat(uy), rat(d))
	dx := new(big.Rat).Sub(cx, rat(int64(a.X)))
	dy := new(big.Rat).Sub(cy, rat(int64(a.Y)))
	r2 := new(big.Rat).Add(new(big.Rat).Mul(dx, dx), new(big.Rat).Mul(dy, dy))
	return Circle{cx, cy, r2, []Point{a, b, c}}
}

// dist2To 返回 p 到圆心距离的平方。
func (c Circle) dist2To(p Point) *big.Rat {
	dx := new(big.Rat).Sub(c.Cx, rat(int64(p.X)))
	dy := new(big.Rat).Sub(c.Cy, rat(int64(p.Y)))
	return dx.Add(dx.Mul(dx, dx), dy.Mul(dy, dy))
}

// Contains 报告 p 是否在圆内或圆上。
func (c Circle) Contains(p Point) bool { return c.dist2To(p).Cmp(c.R2) <= 0 }

// OnBoundary 报告 p 是否恰在圆上。
func (c Circle) OnBoundary(p Point) bool { return c.dist2To(p).Cmp(c.R2) == 0 }

// Equal 报告两圆的圆心与 r² 是否逐字段相等。
func (c Circle) Equal(o Circle) bool {
	return c.Cx.Cmp(o.Cx) == 0 && c.Cy.Cmp(o.Cy) == 0 && c.R2.Cmp(o.R2) == 0
}
