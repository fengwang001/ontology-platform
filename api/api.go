// Package api 是 HLL 近似基数估计的对外门面：New/Add/Estimate/Merge/SelfCheck。
package api

import (
	"errors"
	"fmt"
	"math"

	"ontology/hll"
)

// 三类可判定哨兵错误，互不相同。
var (
	ErrBadPrecision = hll.ErrBadPrecision // p 不在 [4,16]
	ErrMismatch     = hll.ErrMismatch     // 合并两个 p 不同的 sketch
	ErrNotInit      = hll.ErrNotInit      // 零值 sketch（未经 New 构造）
)

// Sketch 是对外句柄，零值不可用（所有操作返回 ErrNotInit 且不改状态）。
type Sketch struct {
	inner *hll.Sketch
}

// New 以精度 p（4<=p<=16，m=2^p 个寄存器）构造 sketch。
func New(p int) (*Sketch, error) {
	in, err := hll.New(p)
	if err != nil {
		return nil, err
	}
	return &Sketch{inner: in}, nil
}

// Add 加入一个 key；零值 sketch 返回 ErrNotInit。
func (s *Sketch) Add(key string) error { return s.inner.Add(key) }

// Estimate 返回当前去重基数估计（含小基数线性计数修正）。
func (s *Sketch) Estimate() (float64, error) { return s.inner.Estimate() }

// Merge 把 o 并入 s（逐桶取 max）；p 不同返回 ErrMismatch 且双方状态不变。
func (s *Sketch) Merge(o *Sketch) error {
	if s.inner == nil || o == nil || o.inner == nil {
		return ErrNotInit
	}
	return s.inner.Merge(o.inner)
}

// Registers 返回寄存器快照（供逐桶比对）；零值返回 nil。
func (s *Sketch) Registers() []uint8 {
	if s.inner == nil {
		return nil
	}
	return s.inner.Registers()
}

// SelfCheck 用内置确定性 key 序列核验四条不变量，全部通过返回 nil。
func (s *Sketch) SelfCheck() error {
	keys := make([]string, 2000)
	for i := range keys {
		keys[i] = fmt.Sprintf("selfcheck-key-%d", i)
	}
	// 不变量 1：合并 == 逐一添加；交换律；幂等。
	half := len(keys) / 2
	a, _ := New(10)
	b, _ := New(10)
	whole, _ := New(10)
	for i, k := range keys {
		if i < half {
			a.Add(k)
		} else {
			b.Add(k)
		}
		whole.Add(k)
	}
	ba, _ := New(10)
	ba.Merge(b)
	ba.Merge(a)
	if err := a.Merge(b); err != nil {
		return fmt.Errorf("selfcheck: merge: %w", err)
	}
	if !eqReg(a.inner.Registers(), whole.inner.Registers()) ||
		!eqReg(ba.inner.Registers(), whole.inner.Registers()) {
		return errors.New("selfcheck: invariant1 merge != union")
	}
	if err := a.Merge(a); err != nil || !eqReg(a.inner.Registers(), whole.inner.Registers()) {
		return errors.New("selfcheck: invariant1 merge not idempotent")
	}
	// 不变量 2：估计误差 <= 3σ，σ=1.04/sqrt(m)。
	est, _ := whole.Estimate()
	sigma := 1.04 / math.Sqrt(1024)
	if math.Abs(est-float64(len(keys)))/float64(len(keys)) > 3*sigma {
		return fmt.Errorf("selfcheck: invariant2 estimate %v out of 3σ", est)
	}
	// 不变量 3：寄存器与估计单调不减。
	mono, _ := New(8)
	prev := 0.0
	for _, k := range keys {
		before := mono.inner.Registers()
		mono.Add(k)
		after := mono.inner.Registers()
		for j := range after {
			if after[j] < before[j] {
				return errors.New("selfcheck: invariant3 register decreased")
			}
		}
		e, _ := mono.Estimate()
		if e < prev {
			return errors.New("selfcheck: invariant3 estimate decreased")
		}
		prev = e
	}
	// 不变量 4：失败不留痕。
	if _, err := New(3); !errors.Is(err, ErrBadPrecision) {
		return errors.New("selfcheck: invariant4 bad precision accepted")
	}
	other, _ := New(9)
	snap := whole.inner.Registers()
	if err := whole.Merge(other); !errors.Is(err, ErrMismatch) {
		return errors.New("selfcheck: invariant4 mismatch accepted")
	}
	if !eqReg(snap, whole.inner.Registers()) {
		return errors.New("selfcheck: invariant4 state changed after rejection")
	}
	var zero Sketch
	if err := zero.Add("x"); !errors.Is(err, ErrNotInit) {
		return errors.New("selfcheck: invariant4 zero value accepted")
	}
	if _, err := zero.Estimate(); !errors.Is(err, ErrNotInit) {
		return errors.New("selfcheck: invariant4 zero estimate accepted")
	}
	if err := zero.Merge(whole); !errors.Is(err, ErrNotInit) {
		return errors.New("selfcheck: invariant4 zero merge accepted")
	}
	return nil
}

func eqReg(a, b []uint8) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
