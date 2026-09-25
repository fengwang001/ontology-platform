// Package hll 实现 HyperLogLog 寄存器、Add、Estimate（原始估计 + 小基数线性计数）与 Merge。
package hll

import (
	"errors"
	"math"
	"sync"
	"sync/atomic"

	"ontology/hsh"
)

// 三类可判定的哨兵错误（api 包原样转出）。
var (
	ErrBadPrecision = errors.New("hll: precision p must be in [4,16]")
	ErrMismatch     = errors.New("hll: cannot merge sketches with different precision")
	ErrNotInit      = errors.New("hll: sketch not constructed via New")
)

// Sketch 是一组 m=2^p 个寄存器；Z 与 V 增量维护，Estimate 为 O(1)。
type Sketch struct {
	p, v int // 精度与空寄存器数
	reg  []uint8
	z    float64 // Σ 2^(-ρ)
	// estReads 记录最近一次 Estimate 实际读取的寄存器个数（非导出，测试包内断言）。
	estReads atomic.Int64
	// floor 是 Estimate 返回值的高水位：V 1→0 瞬间线性计数失效可能回落，用它兜底单调。
	floor atomic.Uint64
	mu    sync.RWMutex
}

// New 以精度 p（m=2^p，4<=p<=16）构造 sketch。
func New(p int) (*Sketch, error) {
	if p < 4 || p > 16 {
		return nil, ErrBadPrecision
	}
	m := 1 << uint(p)
	return &Sketch{p: p, reg: make([]uint8, m), z: float64(m), v: m}, nil
}

func (s *Sketch) ready() bool { return s != nil && s.reg != nil }

// Alpha 返回标准偏差修正系数（标准表）。
func Alpha(p int) float64 {
	switch p {
	case 4:
		return 0.673
	case 5:
		return 0.697
	case 6:
		return 0.709
	}
	m := float64(uint64(1) << uint(p))
	return 0.7213 / (1 + 1.079/m)
}

// Add 加入一个 key：register[j] = max(register[j], ρ)，并增量更新 Z、V。
func (s *Sketch) Add(key string) error {
	if !s.ready() {
		return ErrNotInit
	}
	h := hsh.Hash(key)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bump(hsh.Bucket(h, s.p), hsh.Rho(h, s.p))
	return nil
}

// bump 调用方持写锁；只在秩更大时写寄存器，保证单调。
func (s *Sketch) bump(j int, r uint8) {
	if r <= s.reg[j] {
		return
	}
	s.z += math.Pow(2, -float64(r)) - math.Pow(2, -float64(s.reg[j]))
	if s.reg[j] == 0 {
		s.v--
	}
	s.reg[j] = r
}

// Merge 逐桶取 max 并入；先全部校验再改状态；先拷贝 o 的寄存器避免嵌套持锁死锁。
func (s *Sketch) Merge(o *Sketch) error {
	if !s.ready() || !o.ready() {
		return ErrNotInit
	}
	if s.p != o.p {
		return ErrMismatch
	}
	o.mu.RLock()
	src := append([]uint8(nil), o.reg...)
	o.mu.RUnlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	for j := range s.reg {
		s.bump(j, src[j])
	}
	return nil
}

// rawLocked 计算原始估计 E = alpha_m * m^2 / Z，调用方须持读锁。
func (s *Sketch) rawLocked() float64 {
	m := float64(len(s.reg))
	return Alpha(s.p) * m * m / s.z
}

// EstimateRaw 返回原始估计（不做小基数修正）。
func (s *Sketch) EstimateRaw() (float64, error) {
	if !s.ready() {
		return 0, ErrNotInit
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	s.estReads.Store(0) // 只读增量字段，不读任何寄存器
	return s.rawLocked(), nil
}

// Estimate 返回对外估计：E<=2.5m 且 V>0 时用线性计数 m*ln(m/V)。
// 返回值对高水位取 max，保证任何 Add/Merge 后单调不减。
func (s *Sketch) Estimate() (float64, error) {
	if !s.ready() {
		return 0, ErrNotInit
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	s.estReads.Store(0)
	m := float64(len(s.reg))
	e := s.rawLocked()
	if e <= 2.5*m && s.v > 0 {
		e = m * math.Log(m/float64(s.v))
	}
	for { // 与高水位取 max（CAS 自旋，防并发 Estimate 各自读到旧值）
		b := s.floor.Load()
		if cur := math.Float64frombits(b); e <= cur {
			return cur, nil
		}
		if s.floor.CompareAndSwap(b, math.Float64bits(e)) {
			return e, nil
		}
	}
}

// Registers 返回寄存器快照（供自检逐桶比对）；零值 sketch 返回 nil。
func (s *Sketch) Registers() []uint8 {
	if !s.ready() {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]uint8(nil), s.reg...)
}
