// Package circ 由边界点精确构造圆：圆心为有理数，半径以平方表示，全程不用浮点。
package circ

import "math/big"

// Point 是整数坐标的二维点。
type Point struct {
	X, Y int
}

// Circle 以有理数圆心与半径平方表示圆，B 为确定该圆的 1/2/3 个边界点。
type Circle struct {
	Cx, Cy *big.Rat
	R2     *big.Rat
	B      []Point
}

func rat(v int) *big.Rat { return new(big.Rat).SetInt64(int64(v)) }

func dist2(a, b Point) int {
	dx, dy := a.X-b.X, a.Y-b.Y
	return dx*dx + dy*dy
}

// Orient 返回三点转向：正为逆时针，负为顺时针，零为共线。
func Orient(a, b, c Point) int {
	v := (b.X-a.X)*(c.Y-a.Y) - (b.Y-a.Y)*(c.X-a.X)
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
	return Circle{Cx: rat(p.X), Cy: rat(p.Y), R2: rat(0), B: []Point{p}}
}

// FromDiameter 返回以 a、b 为直径的圆：圆心为中点，r² 为距离平方的四分之一。
func FromDiameter(a, b Point) Circle {
	two, four := big.NewRat(2, 1), big.NewRat(4, 1)
	cx := new(big.Rat).Quo(new(big.Rat).Add(rat(a.X), rat(b.X)), two)
	cy := new(big.Rat).Quo(new(big.Rat).Add(rat(a.Y), rat(b.Y)), two)
	r2 := new(big.Rat).Quo(rat(dist2(a, b)), four)
	return Circle{Cx: cx, Cy: cy, R2: r2, B: []Point{a, b}}
}

// FromThree 返回三点确定的最小包围圆：共线回退为两端点直径圆，
// 钝角三角形回退为最长边直径圆，否则取外接圆。
func FromThree(a, b, c Point) Circle {
	if Orient(a, b, c) == 0 { // 共线：最远的一对即两端点
		best, ab := FromDiameter(a, b), dist2(a, b)
		if d := dist2(a, c); d > ab {
			best, ab = FromDiameter(a, c), d
		}
		if d := dist2(b, c); d > ab {
			best = FromDiameter(b, c)
		}
		return best
	}
	// 顶点 v 处钝角 ⟺ (p-v)·(q-v) < 0，此时对边为最长边。
	obtuse := func(v, p, q Point) bool {
		return (p.X-v.X)*(q.X-v.X)+(p.Y-v.Y)*(q.Y-v.Y) < 0
	}
	switch {
	case obtuse(a, b, c):
		return FromDiameter(b, c)
	case obtuse(b, c, a):
		return FromDiameter(a, c)
	case obtuse(c, a, b):
		return FromDiameter(a, b)
	}
	// 外接圆：解垂直平分线交点，分子分母均为整数，精确构造有理数。
	sq := func(p Point) *big.Rat { return rat(p.X*p.X + p.Y*p.Y) }
	sa, sb, sc := sq(a), sq(b), sq(c)
	d := rat(2 * ((b.X-a.X)*(c.Y-a.Y) - (b.Y-a.Y)*(c.X-a.X)))
	num := func(u, v, w int) *big.Rat {
		t := new(big.Rat).Mul(sa, rat(u))
		t.Add(t, new(big.Rat).Mul(sb, rat(v)))
		return t.Add(t, new(big.Rat).Mul(sc, rat(w)))
	}
	cx := new(big.Rat).Quo(num(b.Y-c.Y, c.Y-a.Y, a.Y-b.Y), d)
	cy := new(big.Rat).Quo(num(c.X-b.X, a.X-c.X, b.X-a.X), d)
	dx := new(big.Rat).Sub(rat(a.X), cx)
	dy := new(big.Rat).Sub(rat(a.Y), cy)
	r2 := new(big.Rat).Add(new(big.Rat).Mul(dx, dx), new(big.Rat).Mul(dy, dy))
	return Circle{Cx: cx, Cy: cy, R2: r2, B: []Point{a, b, c}}
}

// dist2c 返回 p 到圆心距离的平方。
func (c Circle) dist2c(p Point) *big.Rat {
	dx := new(big.Rat).Sub(rat(p.X), c.Cx)
	dy := new(big.Rat).Sub(rat(p.Y), c.Cy)
	return dx.Add(dx.Mul(dx, dx), dy.Mul(dy, dy))
}

// Contains 报告 p 是否落在圆内或圆上。
func (c Circle) Contains(p Point) bool { return c.dist2c(p).Cmp(c.R2) <= 0 }

// OnBoundary 报告 p 是否恰在圆上。
func (c Circle) OnBoundary(p Point) bool { return c.dist2c(p).Cmp(c.R2) == 0 }
