// Package api 是 MinHash 相似度估计器的对外接口。
package api

import (
	"errors"
	"fmt"

	"ontology/hash"
	"ontology/mh"
)

// 四类可判定错误，互不相同。
var (
	ErrBadK         = errors.New("api: k must be > 0 and equal to len(hs)")
	ErrHashParams   = errors.New("api: invalid hash params")
	ErrHashMismatch = errors.New("api: hash function sets differ")
	ErrEmptySet     = errors.New("api: cannot estimate with an empty set")
)

// Sketch 是对外句柄，内部状态在 mh 包。
type Sketch struct{ inner *mh.Sketch }

// NewSketch 校验 k 与每个哈希函数的参数；任何非法都整体失败、不留状态。
func NewSketch(k int, hs []hash.Hash) (*Sketch, error) {
	if k <= 0 || len(hs) != k {
		return nil, ErrBadK
	}
	for _, h := range hs {
		if h == nil || hash.Validate(h) != nil {
			return nil, ErrHashParams
		}
	}
	return &Sketch{inner: mh.New(hs)}, nil
}

// Add 把元素并入签名。
func (s *Sketch) Add(x uint64) { s.inner.Add(x) }

// Signature 返回签名副本。
func (s *Sketch) Signature() []uint64 { return s.inner.Signature() }

// Estimate 估计 Jaccard 相似度 = 签名相等位置数 / k。
// 先校验（哈希函数集一致、双方非空），失败时不改变任何状态。
func Estimate(a, b *Sketch) (float64, error) {
	if !a.inner.SameHashes(b.inner) {
		return 0, ErrHashMismatch
	}
	if a.inner.Empty() || b.inner.Empty() {
		return 0, ErrEmptySet
	}
	return float64(mh.EqualPositions(a.inner, b.inner)) / float64(a.inner.K()), nil
}

// SelfCheck 用内置集合与哈希函数核验四条不变量，全部通过返回 nil。
func SelfCheck() error {
	mk := func() (*Sketch, *Sketch, error) {
		hs := make([]hash.Hash, 4)
		params := [][3]uint64{{2, 3, 11}, {3, 1, 11}, {5, 4, 11}, {7, 2, 11}}
		for i, p := range params {
			h, err := hash.New(p[0], p[1], p[2])
			if err != nil {
				return nil, nil, err
			}
			hs[i] = h
		}
		a, err := NewSketch(4, hs)
		if err != nil {
			return nil, nil, err
		}
		b, err := NewSketch(4, hs)
		if err != nil {
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
	a1, b1, err := mk()
	if err != nil {
		return err
	}
	a2, b2, err := mk()
	if err != nil {
		return err
	}
	// 不变量 1+3：签名正确且确定性（逐位等于已知答案，两次构建相同）。
	wantA := []uint64{0, 0, 2, 7}
	wantB := []uint64{0, 2, 0, 3}
	for i := range wantA {
		if a1.Signature()[i] != wantA[i] || b1.Signature()[i] != wantB[i] {
			return fmt.Errorf("selfcheck: signature mismatch at %d", i)
		}
		if a1.Signature()[i] != a2.Signature()[i] || b1.Signature()[i] != b2.Signature()[i] {
			return fmt.Errorf("selfcheck: non-deterministic at %d", i)
		}
	}
	// 不变量 2+3：与朴素参照一致（1/4）且对称。
	ab, err := Estimate(a1, b1)
	if err != nil {
		return err
	}
	ba, err := Estimate(b1, a1)
	if err != nil {
		return err
	}
	if ab != 0.25 || ab != ba {
		return fmt.Errorf("selfcheck: estimate %v / %v, want 0.25 symmetric", ab, ba)
	}
	// 不变量 4：被拒操作不改变状态。
	empty, err := NewSketch(4, mustHashes())
	if err != nil {
		return err
	}
	if _, err := Estimate(a1, empty); !errors.Is(err, ErrEmptySet) {
		return fmt.Errorf("selfcheck: want ErrEmptySet, got %v", err)
	}
	if _, err := NewSketch(0, nil); !errors.Is(err, ErrBadK) {
		return fmt.Errorf("selfcheck: want ErrBadK, got %v", err)
	}
	for i := range wantA {
		if a1.Signature()[i] != wantA[i] {
			return fmt.Errorf("selfcheck: state changed after rejected op")
		}
	}
	return nil
}

func mustHashes() []hash.Hash {
	hs := make([]hash.Hash, 4)
	for i, p := range [][3]uint64{{2, 3, 11}, {3, 1, 11}, {5, 4, 11}, {7, 2, 11}} {
		h, err := hash.New(p[0], p[1], p[2])
		if err != nil {
			panic(err)
		}
		hs[i] = h
	}
	return hs
}
