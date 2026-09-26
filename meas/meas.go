// Package meas 提供简单多边形的度量：鞋带面积、一阶矩、重心有理数。
// 依赖 poly；全程整数运算，不用浮点。
package meas

import (
	"sync/atomic"

	"ontology/poly"
)

// Rat 是既约有理数 Num/Den，约定 Den > 0。
type Rat struct {
	Num, Den int64
}

func gcd(a, b int64) int64 {
	if a < 0 {
		a = -a
	}
	if b < 0 {
		b = -b
	}
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// NewRat 返回既约且分母为正的 num/den。den 为 0 时 panic（调用方保证不发生）。
func NewRat(num, den int64) Rat {
	if den == 0 {
		panic("meas: zero denominator")
	}
	if den < 0 {
		num, den = -num, -den
	}
	g := gcd(num, den)
	return Rat{Num: num / g, Den: den / g}
}

// PointRat 是两个有理数坐标组成的点。
type PointRat struct {
	X, Y Rat
}

// Polygon 是不可变顶点序列上的度量器，可并发只读使用。
type Polygon struct {
	verts []poly.Point
	reads int64 // 非导出计数器：最近一次 Area/Centroid 实际读取的顶点个数（仅经 atomic 访问）
}

// NewPolygon 复制顶点切片并返回度量器。
func NewPolygon(verts []poly.Point) *Polygon {
	cp := make([]poly.Point, len(verts))
	copy(cp, verts)
	return &Polygon{verts: cp}
}

// measure 沿边单趟扫描：二倍有向面积 a2、一阶矩 mx/my。
// 每条边读一次、每顶点 O(1)；把实际读取的顶点数记入非导出计数器。
func (p *Polygon) measure() (a2, mx, my int64) {
	n := len(p.verts)
	var cnt int64
	for i := 0; i < n; i++ {
		a, b := p.verts[i], p.verts[(i+1)%n]
		cnt++ // 每条边处理一次，恰好覆盖 n 个顶点
		c := poly.Cross(a, b)
		a2 += c
		mx += (a.X + b.X) * c
		my += (a.Y + b.Y) * c
	}
	atomic.StoreInt64(&p.reads, cnt)
	return a2, mx, my
}

// Area 返回鞋带面积 Σc/2 的既约有理数（逆时针输入为正）。
func (p *Polygon) Area() Rat {
	a2, _, _ := p.measure()
	return NewRat(a2, 2)
}

// Centroid 返回重心 (Σ(x+x')c/(6A), Σ(y+y')c/(6A))；因 6A = 3·(2A)，分母取 3·a2。
func (p *Polygon) Centroid() PointRat {
	a2, mx, my := p.measure()
	return PointRat{X: NewRat(mx, 3*a2), Y: NewRat(my, 3*a2)}
}
