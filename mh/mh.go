// Package mh 维护 MinHash 签名：对每个注入的哈希函数取集合内哈希值的最小值。
package mh

import (
	"errors"
	"math"
	"sync"

	"ontology/hash"
)

var (
	// ErrBadK 表示签名长度非法（k <= 0）。
	ErrBadK = errors.New("mh: k must be positive")
	// ErrHashMismatch 表示两个 sketch 的 (a,b,p) 序列不同，不能比较。
	ErrHashMismatch = errors.New("mh: hash function sets differ")
	// ErrEmpty 表示对含空集合的一方调用 Estimate。
	ErrEmpty = errors.New("mh: cannot estimate with an empty set")
)

// Sketch 是一个集合的 MinHash 签名。
type Sketch struct {
	mu    sync.RWMutex
	hs    []hash.Hash
	sig   []uint64
	n     int // 已 Add 的元素个数
	evals int // Add 中哈希求值的总次数（非导出，不进公开接口）
}

// New 用给定的哈希函数集创建空签名，每位初值为 MaxUint64（+Inf）。
// k <= 0 时整体失败，不产生任何状态。
func New(hs []hash.Hash) (*Sketch, error) {
	if len(hs) == 0 {
		return nil, ErrBadK
	}
	sig := make([]uint64, len(hs))
	for i := range sig {
		sig[i] = math.MaxUint64
	}
	return &Sketch{hs: hs, sig: sig}, nil
}

// Add 把元素 x 并入签名：对每个 i 恰好求值一次并取 min。单次 Add 恰好 k 次求值，与集合规模无关。
func (s *Sketch) Add(x uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, h := range s.hs {
		if v := h.Eval(x); v < s.sig[i] {
			s.sig[i] = v
		}
		s.evals++
	}
	s.n++
}

// Signature 返回签名副本；空集合每位都是 MaxUint64。
func (s *Sketch) Signature() []uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]uint64, len(s.sig))
	copy(out, s.sig)
	return out
}

// Empty 报告集合是否为空（从未 Add）。
func (s *Sketch) Empty() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.n == 0
}

// sameHashes 报告两个 sketch 的 (a,b,p) 序列是否逐项相同。
func sameHashes(a, b *Sketch) bool {
	if len(a.hs) != len(b.hs) {
		return false
	}
	for i := range a.hs {
		aa, ab, ap := a.hs[i].Params()
		ba, bb, bp := b.hs[i].Params()
		if aa != ba || ab != bb || ap != bp {
			return false
		}
	}
	return true
}

// Estimate 返回两签名相等位置数 / k。哈希函数集不同或任一方为空集合时整体失败。
// 所有校验先于任何读取之外的副作用；本函数只读，不改任何状态。
func Estimate(a, b *Sketch) (float64, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a != b {
		b.mu.RLock()
		defer b.mu.RUnlock()
	}
	if !sameHashes(a, b) {
		return 0, ErrHashMismatch
	}
	if a.n == 0 || b.n == 0 {
		return 0, ErrEmpty
	}
	eq := 0
	for i := range a.sig {
		if a.sig[i] == b.sig[i] {
			eq++
		}
	}
	return float64(eq) / float64(len(a.sig)), nil
}
