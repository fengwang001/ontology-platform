// Package hyper 实现可复现的随机超平面族与签名计算。
package hyper

import (
	"math"
	"math/rand/v2"

	"ontology/vec"
)

// Family 是 L 张表、每表 b 个、每个 d 维的法向量族。
type Family struct {
	Dim      int
	Tables   int
	Bits     int
	Normals  [][][]float64 // [table][bit][dim]
	HashOps  int           // 自创建以来计算的内积（哈希位）总数
}

// NewFamily 用显式种子生成法向量。同一 (seed,dim,L,b) 逐字节相同。
// 法向量分量取标准正态；生成顺序固定：表 → 位 → 维度。
func NewFamily(seed uint64, dim, tables, bits int) *Family {
	r := rand.NewPCG(seed, seed)
	norm := make([][][]float64, tables)
	for l := 0; l < tables; l++ {
		norm[l] = make([][]float64, bits)
		for j := 0; j < bits; j++ {
			n := make([]float64, dim)
			for k := range n {
				n[k] = r.NormFloat64()
			}
			norm[l][j] = n
		}
	}
	return &Family{Dim: dim, Tables: tables, Bits: bits, Normals: norm}
}

// Signature 计算某张表内向量 v 的 b 位签名（位 j 对应超平面 j）。
// 约定：dot>0 取 1；dot<=0（含恰为 0，如零向量）取 0。
func (f *Family) Signature(v vec.Vec, table int) uint64 {
	var sig uint64
	for j := 0; j < f.Bits; j++ {
		f.HashOps++
		if vec.Dot(v, f.Normals[table][j]) > 0 {
			sig |= 1 << uint(j)
		}
	}
	return sig
}

// Signatures 一次返回 L 张表的签名，并累计 L*b 次哈希运算。
func (f *Family) Signatures(v vec.Vec) []uint64 {
	sigs := make([]uint64, f.Tables)
	for l := 0; l < f.Tables; l++ {
		sigs[l] = f.Signature(v, l)
	}
	return sigs
}

// IsDegenerate 判断某法向量是否全零（无法划分超平面），返回表号与位号。
func (f *Family) IsDegenerate() (table, bit int, ok bool) {
	for l := range f.Normals {
		for j := range f.Normals[l] {
			zero := true
			for _, x := range f.Normals[l][j] {
				if x != 0 || math.IsNaN(x) {
					zero = false
					break
				}
			}
			if zero {
				return l, j, true
			}
		}
	}
	return 0, 0, false
}
