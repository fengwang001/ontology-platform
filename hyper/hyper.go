// Package hyper 实现可复现的随机超平面族与签名计算。
package hyper

import (
	"math/rand"
	"sync/atomic"

	"ontology/vec"
)

// Family 是一组 b 个随机超平面，可为 d 维向量计算 b 位签名。
// 法向量由显式种子生成：同种子两次构造得到逐字节相同的法向量。
type Family struct {
	dim    int
	planes [][]float64 // bits 个法向量，每个 dim 维
	hashes atomic.Int64
}

// New 以 seed 生成 dim 维、bits 位的超平面族。
// 随机流被顺序消费，因此同种子下 bits 较小的族是较大族的前缀。
func New(dim, bits int, seed int64) *Family {
	if dim < 1 || bits < 1 || bits > 64 {
		panic("hyper: bad dim/bits")
	}
	r := rand.New(rand.NewSource(seed))
	planes := make([][]float64, bits)
	for i := range planes {
		p := make([]float64, dim)
		for j := range p {
			p[j] = r.NormFloat64()
		}
		planes[i] = p
	}
	return &Family{dim: dim, planes: planes}
}

// Dim 返回向量维度，Bits 返回签名位数。
func (f *Family) Dim() int { return f.dim }

// Bits 返回签名位数。
func (f *Family) Bits() int { return len(f.planes) }

// Planes 返回全部法向量（只读使用）。
func (f *Family) Planes() [][]float64 { return f.planes }

// Hashes 返回该族累计执行的哈希（内积）计算次数。
func (f *Family) Hashes() int64 { return f.hashes.Load() }

// Signature 计算 v 的签名：bit_i = 1 当且仅当 dot(plane_i, v) > 0，
// 内积为 0（如零向量）时该位取 0。调用方需保证维度匹配。
func (f *Family) Signature(v vec.Vec) uint64 {
	var sig uint64
	for i, p := range f.planes {
		f.hashes.Add(1)
		if vec.Dot(p, v) > 0 {
			sig |= 1 << uint(i)
		}
	}
	return sig
}
