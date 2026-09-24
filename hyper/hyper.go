// Package hyper 提供可复现的随机超平面族与 LSH 签名计算。
package hyper

import (
	"math/rand"
	"sync/atomic"

	"ontology/vec"
)

// Family 是 L 张表、每张表 b 个超平面的随机超平面族。
type Family struct {
	Dim    int
	Tables int
	Bits   int
	Seed   int64
	// normals[t][i] 为第 t 张表第 i 位的法向量。
	normals [][][]float64
	hashOps int64
}

// NewFamily 用显式种子按固定顺序生成法向量，保证可复现。
func NewFamily(dim, tables, bits int, seed int64) *Family {
	f := &Family{
		Dim: dim, Tables: tables, Bits: bits, Seed: seed,
		normals: make([][][]float64, tables),
	}
	rng := rand.New(rand.NewSource(seed))
	for t := 0; t < tables; t++ {
		f.normals[t] = make([][]float64, bits)
		for i := 0; i < bits; i++ {
			n := make([]float64, dim)
			for d := 0; d < dim; d++ {
				n[d] = rng.NormFloat64()
			}
			f.normals[t][i] = n
		}
	}
	return f
}

// Normals 返回法向量的只读副本，供落盘/校验使用。
func (f *Family) Normals() [][][]float64 {
	out := make([][][]float64, f.Tables)
	for t := range out {
		out[t] = make([][]float64, f.Bits)
		for i := range out[t] {
			out[t][i] = append([]float64(nil), f.normals[t][i]...)
		}
	}
	return out
}

// FromNormals 由给定法向量重建族（用于读盘）。
func FromNormals(seed int64, normals [][][]float64) *Family {
	f := &Family{Seed: seed, normals: normals}
	if len(normals) > 0 {
		f.Tables = len(normals)
		f.Bits = len(normals[0])
		if len(normals[0]) > 0 {
			f.Dim = len(normals[0][0])
		}
	}
	return f
}

// IsZero 判断第 t 张表第 i 位法向量是否全零（退化超平面）。
func (f *Family) IsZero(t, i int) bool {
	for _, x := range f.normals[t][i] {
		if x != 0 {
			return false
		}
	}
	return true
}

// Signature 计算 x 在第 t 张表的 b 位签名，不计入建索引哈希计数。
func (f *Family) Signature(t int, x []float64) (uint64, error) {
	return f.signature(t, x, false)
}

// IndexSignatures 建索引路径：计算全部表签名，并精确计数（每向量 L×b 次）。
func (f *Family) IndexSignatures(x []float64) ([]uint64, error) {
	sigs := make([]uint64, f.Tables)
	for t := 0; t < f.Tables; t++ {
		s, err := f.signature(t, x, true)
		if err != nil {
			return nil, err
		}
		sigs[t] = s
	}
	return sigs, nil
}

func (f *Family) signature(t int, x []float64, count bool) (uint64, error) {
	if len(x) != f.Dim {
		return 0, vec.ErrDim
	}
	var sig uint64
	for i := 0; i < f.Bits; i++ {
		d, err := vec.Dot(x, f.normals[t][i])
		if err != nil {
			return 0, err
		}
		if d > 0 { // 内积恰为 0（含零向量）约定为 0
			sig |= uint64(1) << uint(i)
		}
		if count {
			atomic.AddInt64(&f.hashOps, 1)
		}
	}
	return sig, nil
}

// HashOps 返回建索引以来累计的哈希（内积）计算次数。
func (f *Family) HashOps() int64 { return atomic.LoadInt64(&f.hashOps) }
