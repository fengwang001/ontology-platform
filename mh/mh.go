// Package mh 维护 MinHash 签名：对每个哈希函数取集合内哈希值的最小值。
package mh

import (
	"math"

	"ontology/hash"
)

// Sketch 是一个集合的 MinHash 签名。构造后仅 Add 会修改状态。
type Sketch struct {
	hs    []hash.Hash
	sig   []uint64
	n     uint64 // 已 Add 的元素个数，用于判定空集合
	evals uint64 // 非导出计数器：Add 中哈希求值次数，不出现在公开接口
}

// New 以给定哈希函数集创建空签名，每个位置初始为 MaxUint64（+Inf）。
func New(hs []hash.Hash) *Sketch {
	sig := make([]uint64, len(hs))
	for i := range sig {
		sig[i] = math.MaxUint64
	}
	return &Sketch{hs: hs, sig: sig}
}

// Add 把元素 x 并入签名：对每个 i 做恰好一次哈希求值并取最小。
func (s *Sketch) Add(x uint64) {
	for i, h := range s.hs {
		v := h.Eval(x)
		s.evals++
		if v < s.sig[i] {
			s.sig[i] = v
		}
	}
	s.n++
}

// Signature 返回签名副本（调用方改动不影响内部状态）。
func (s *Sketch) Signature() []uint64 {
	return append([]uint64(nil), s.sig...)
}

// Empty 报告是否尚未 Add 任何元素。
func (s *Sketch) Empty() bool { return s.n == 0 }

// K 返回签名长度。
func (s *Sketch) K() int { return len(s.sig) }

// SameHashes 报告两个 sketch 的 (a,b,p) 序列是否逐项相同。
func (s *Sketch) SameHashes(o *Sketch) bool {
	if len(s.hs) != len(o.hs) {
		return false
	}
	for i, h := range s.hs {
		a1, b1, p1 := h.Params()
		a2, b2, p2 := o.hs[i].Params()
		if a1 != a2 || b1 != b2 || p1 != p2 {
			return false
		}
	}
	return true
}

// EqualPositions 数两个签名相等的位置数；调用方需先保证 SameHashes。
func EqualPositions(a, b *Sketch) int {
	sa, sb := a.sig, b.sig
	n := 0
	for i := range sa {
		if sa[i] == sb[i] {
			n++
		}
	}
	return n
}
