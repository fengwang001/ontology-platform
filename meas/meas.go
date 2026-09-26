// Package meas 提供简单多边形的度量：鞋带面积、一阶矩、重心有理数。
// 依赖 poly；全部用整数有理数，不引入浮点。
package meas

import "ontology/poly"

// Rat 是规范化有理数：Den > 0，gcd(|Num|, Den) = 1。
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
	if a == 0 {
		return 1
	}
	return a
}

// NewRat 构造规范化有理数；den 为负时把符号并入分子。
func NewRat(num, den int64) Rat {
	if den < 0 {
		num, den = -num, -den
	}
	g := gcd(num, den)
	return Rat{num / g, den / g}
}

// Add 返回 r+s。
func Add(r, s Rat) Rat { return NewRat(r.Num*s.Den+s.Num*r.Den, r.Den*s.Den) }

// Mul 返回 r*s。
func Mul(r, s Rat) Rat { return NewRat(r.Num*s.Num, r.Den*s.Den) }

// Eq 报告两有理数是否相等（均已规范化，逐字段比较即可）。
func Eq(r, s Rat) bool { return r.Num == s.Num && r.Den == s.Den }

// PointRat 是有理数坐标点。
type PointRat struct {
	X, Y Rat
}

// measure 沿边扫描一次，返回鞋带和 area2、一阶矩 mx/my 与实际读取计数。
// reads 是非导出计数器：每条边读一次，m 边形恰好为 m，证明 O(n) 无重复遍历。
func measure(verts []poly.Point) (area2, mx, my int64, reads int) {
	n := len(verts)
	for i := 0; i < n; i++ {
		a, b := verts[i], verts[(i+1)%n]
		c := poly.Cross(a, b)
		area2 += c
		mx += (a.X + b.X) * c
		my += (a.Y + b.Y) * c
		reads++
	}
	return area2, mx, my, reads
}

// Area 返回面积有理数 = 鞋带和/2。调用方须保证输入逆时针（area2 > 0）。
func Area(verts []poly.Point) Rat {
	area2, _, _, _ := measure(verts)
	return NewRat(area2, 2)
}

// Centroid 返回重心有理数点：Cx = mx/(3·area2)，Cy = my/(3·area2)（即除以 6A）。
func Centroid(verts []poly.Point) PointRat {
	area2, mx, my, _ := measure(verts)
	return PointRat{NewRat(mx, 3*area2), NewRat(my, 3*area2)}
}

// VerifyLinearReads 自检：对若干规模 m，一次扫描的读取计数恰好等于 m。
// 只返回结论，计数器数值不跨出包边界。
func VerifyLinearReads(sizes ...int) bool {
	for _, m := range sizes {
		v := make([]poly.Point, m)
		for i := range v {
			v[i] = poly.Point{X: int64(i), Y: int64(i * i)}
		}
		if _, _, _, reads := measure(v); reads != m {
			return false
		}
	}
	return true
}
