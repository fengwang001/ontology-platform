// Package hyper 提供可复现的随机超平面族与签名计算（CharHash 风格 LSH）。
package hyper

import (
	"math"
	"math/rand"

	"ontology/vec"
)

// Family 是 L 张表、每表 b 个法向量的随机超平面族，法向量连续存放。
type Family struct {
	Dim    int
	Bits   int
	Tables int
	Seed   int64
	planes []float64 // 长度 Tables*Bits*Dim；[t*Bits+q]*Dim 起为一条法向量
}

// Signature 是 b 位签名的无符号整数表示（b<=64）。
type Signature uint64

// newSource 以 (seed, table, bit) 派生独立 source，保证逐位可复现。
func newSource(seed int64, table, bit int) *rand.Rand {
	h := uint64(uint64(seed)+1) * 1099511628211
	h ^= uint64(table+1) * 1469598103934665603
	h ^= uint64(bit+1) * 6364136223846793005
	return rand.New(rand.NewSource(int64(h)))
}

// NewFamily 用显式种子生成超平面族。法向量分量为标准正态（Box-Muller）。
func NewFamily(dim, bits, tables int, seed int64) *Family {
	f := &Family{
		Dim: dim, Bits: bits, Tables: tables, Seed: seed,
		planes: make([]float64, tables*bits*dim),
	}
	off := 0
	for t := 0; t < tables; t++ {
		for q := 0; q < bits; q++ {
			r := newSource(seed, t, q)
			for d := 0; d < dim; d++ {
				u1 := r.Float64()
				u2 := r.Float64()
				if u1 < 1e-15 {
					u1 = 1e-15
				}
				mag := math.Sqrt(-2 * math.Log(u1))
				f.planes[off] = mag * math.Cos(2*math.Pi*u2)
				off++
			}
		}
	}
	return f
}

// Plane 返回第 table 表第 bit 位法向量（只读切片）。
func (f *Family) Plane(table, bit int) vec.Vector {
	off := (table*f.Bits + bit) * f.Dim
	return f.planes[off : off+f.Dim]
}

// IsDegenerate 报告某条法向量是否全零（退化、无法划分）。
func (f *Family) IsDegenerate(table, bit int) bool {
	p := f.Plane(table, bit)
	for _, x := range p {
		if x != 0 {
			return false
		}
	}
	return true
}

// Sign 计算向量在第 table 表的 b 位签名。
// 约定：内积 >= 0（含恰为 0，如零向量）取位 1，否则 0。
func (f *Family) Sign(x vec.Vector, table int) (Signature, error) {
	if err := vec.CheckDim(f.Dim, len(x)); err != nil {
		return 0, err
	}
	var s Signature
	for q := 0; q < f.Bits; q++ {
		dot, _ := vec.Dot(x, f.Plane(table, q))
		if dot >= 0 {
			s |= Signature(1) << uint(q)
		}
	}
	return s, nil
}

// Signs 计算向量在全部 L 张表上的签名。
func (f *Family) Signs(x vec.Vector) ([]Signature, error) {
	out := make([]Signature, f.Tables)
	for t := 0; t < f.Tables; t++ {
		s, err := f.Sign(x, t)
		if err != nil {
			return nil, err
		}
		out[t] = s
	}
	return out, nil
}
