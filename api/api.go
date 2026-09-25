// Package api 是 MinHash 相似度估计器的对外门面。
package api

import (
	"errors"
	"fmt"
	"slices"

	"ontology/hash"
	"ontology/mh"
)

// Hash 是注入的哈希函数 h(x) = (a*x + b) mod p，参数由调用方给定。
type Hash = hash.Hash

// 可判定的哨兵错误，四类互不相同。
var (
	ErrBadK         = mh.ErrBadK         // k <= 0（或哈希函数个数与 k 不符）
	ErrBadParams    = hash.ErrBadParams  // p <= 1 或 a ≡ 0 (mod p)
	ErrHashMismatch = mh.ErrHashMismatch // 两 sketch 的 (a,b,p) 序列不同
	ErrEmpty        = mh.ErrEmpty        // 对含空集合的一方调 Estimate
)

// Sketch 是一个集合的 MinHash 签名，并发只读安全。
type Sketch struct{ inner *mh.Sketch }

// NewSketch 用 k 个注入的哈希函数创建空签名；k <= 0 或个数不符时整体失败。
func NewSketch(k int, hs []Hash) (*Sketch, error) {
	if k <= 0 || len(hs) != k {
		return nil, ErrBadK
	}
	s, err := mh.New(hs)
	if err != nil {
		return nil, err
	}
	return &Sketch{inner: s}, nil
}

// Add 把元素 x 并入签名。
func (s *Sketch) Add(x uint64) { s.inner.Add(x) }

// Signature 返回签名副本；空集合每位都是 MaxUint64（+Inf）。
func (s *Sketch) Signature() []uint64 { return s.inner.Signature() }

// Estimate 返回两签名相等位置数 / k，估计 Jaccard 相似度。只读，不改状态。
func Estimate(a, b *Sketch) (float64, error) { return mh.Estimate(a.inner, b.inner) }

// SelfCheck 对内置集合与哈希函数核验四条不变量，全部通过返回 nil；只用局部状态，可并发调用。
func SelfCheck() error {
	hs := builtins()
	a, b, err := buildAB(hs)
	if err != nil {
		return err
	}
	// 不变量 1：签名正确（八行表结论）。
	if !slices.Equal(a.Signature(), []uint64{0, 0, 2, 7}) || !slices.Equal(b.Signature(), []uint64{0, 2, 0, 3}) {
		return errors.New("selfcheck: signature mismatch")
	}
	est, err1 := Estimate(a, b)
	rev, err2 := Estimate(b, a)
	if err1 != nil || err2 != nil {
		return errors.New("selfcheck: estimate error")
	}
	// 不变量 2（朴素参照一致）与 3（对称）。
	if est != 0.25 || est != rev || est != naive(hs, []uint64{1, 4, 7}, []uint64{1, 4, 8, 9}) {
		return errors.New("selfcheck: estimate mismatch")
	}
	// 不变量 3：确定性，两次构建逐位相同。
	a2, _, err := buildAB(hs)
	if err != nil || !slices.Equal(a.Signature(), a2.Signature()) {
		return errors.New("selfcheck: non-deterministic signature")
	}
	return checkRejections(hs, a) // 不变量 4：失败不留痕
}

func builtins() []Hash {
	params := [][3]uint64{{2, 3, 11}, {3, 1, 11}, {5, 4, 11}, {7, 2, 11}}
	hs := make([]Hash, len(params))
	for i, p := range params {
		hs[i], _ = hash.New(p[0], p[1], p[2]) // 常量参数必合法
	}
	return hs
}

func buildAB(hs []Hash) (a, b *Sketch, err error) {
	if a, err = NewSketch(4, hs); err != nil {
		return nil, nil, err
	}
	if b, err = NewSketch(4, hs); err != nil {
		return nil, nil, err
	}
	for _, x := range []uint64{1, 4, 7} {
		a.Add(x)
	}
	for _, x := range []uint64{1, 4, 8, 9} {
		b.Add(x)
	}
	return a, b, nil
}

// naive 逐元素重算两集合各哈希的最小值，再数相等位置数 / k。
func naive(hs []Hash, sa, sb []uint64) float64 {
	eq := 0
	for _, h := range hs {
		if minOf(h, sa) == minOf(h, sb) {
			eq++
		}
	}
	return float64(eq) / float64(len(hs))
}

func minOf(h Hash, s []uint64) uint64 {
	m := ^uint64(0)
	for _, x := range s {
		if v := h.Eval(x); v < m {
			m = v
		}
	}
	return m
}

func checkRejections(hs []Hash, a *Sketch) error {
	before := a.Signature()
	if _, err := NewSketch(0, hs); !errors.Is(err, ErrBadK) {
		return fmt.Errorf("selfcheck: k<=0 not rejected: %v", err)
	}
	if _, err := hash.New(0, 1, 11); !errors.Is(err, ErrBadParams) {
		return fmt.Errorf("selfcheck: bad params not rejected: %v", err)
	}
	empty, err := NewSketch(4, hs)
	if err != nil {
		return err
	}
	if _, err := Estimate(a, empty); !errors.Is(err, ErrEmpty) {
		return fmt.Errorf("selfcheck: empty not rejected: %v", err)
	}
	other, err := NewSketch(1, hs[:1]) // 哈希函数集不同
	if err != nil {
		return err
	}
	other.Add(1)
	if _, err := Estimate(a, other); !errors.Is(err, ErrHashMismatch) {
		return fmt.Errorf("selfcheck: mismatch not rejected: %v", err)
	}
	if !slices.Equal(before, a.Signature()) {
		return errors.New("selfcheck: rejected op mutated state")
	}
	return nil
}
