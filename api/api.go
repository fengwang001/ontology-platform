// Package api 是 LogLog 基数估计的对外接口。依赖 est。
package api

import (
	"errors"
	"fmt"
	"math"
	"sync"

	"ontology/est"
)

// 可判定的哨兵错误，三者互不相同。
var (
	ErrInvalidM    = errors.New("loglog: m must be a positive power of two")
	ErrBucketRange = errors.New("loglog: bucket out of range")
	ErrNegativeZ   = errors.New("loglog: z must be non-negative")
)

// Sketch 是并发安全的 LogLog 基数估计器。
type Sketch struct {
	mu sync.Mutex
	e  *est.Estimator
	m  int
}

// New 创建 m 个寄存器的 Sketch；m 必须是正的 2 的幂，否则返回 ErrInvalidM。
func New(m int) (*Sketch, error) {
	if m <= 0 || m&(m-1) != 0 {
		return nil, ErrInvalidM
	}
	return &Sketch{e: est.New(m), m: m}, nil
}

// Add 注入一个元素的 (bucket, z)。参数非法时整体失败，不改变任何寄存器。
func (s *Sketch) Add(bucket, z int) error {
	if bucket < 0 || bucket >= s.m {
		return ErrBucketRange
	}
	if z < 0 {
		return ErrNegativeZ
	}
	s.mu.Lock()
	s.e.Add(bucket, z)
	s.mu.Unlock()
	return nil
}

// Estimate 返回基数估计 α_m · m · 2^mean。
func (s *Sketch) Estimate() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.e.Estimate()
}

// Registers 返回全部寄存器的副本。
func (s *Sketch) Registers() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.e.Registers()
}

// LastEstimateReadAll 报告最近一次 Estimate 访问的寄存器个数是否恰好为 m。
func (s *Sketch) LastEstimateReadAll() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.e.LastEstimateReadAll()
}

// SelfCheck 对内置 (bucket,z) 序列核验四条不变量，全部通过返回 nil。
// 只使用内部新建的 Sketch，不触碰接收者状态，可并发调用。
func (s *Sketch) SelfCheck() error {
	seq := [][2]int{{0, 0}, {2, 2}, {2, 4}, {5, 1}, {0, 3}, {5, 5}, {2, 3}}

	// 不变量 1+2：单调不减、单元素精确。
	one, _ := New(8)
	if err := one.Add(3, 2); err != nil {
		return err
	}
	for j, v := range one.Registers() {
		if (j == 3) != (v == 3) {
			return fmt.Errorf("selfcheck: single-element exactness broken at reg[%d]=%d", j, v)
		}
	}
	mono, _ := New(8)
	prev := mono.Registers()
	for _, p := range seq {
		if err := mono.Add(p[0], p[1]); err != nil {
			return err
		}
		cur := mono.Registers()
		for j := range cur {
			if cur[j] < prev[j] {
				return fmt.Errorf("selfcheck: reg[%d] decreased %d -> %d", j, prev[j], cur[j])
			}
		}
		prev = cur
	}

	// 不变量 3：与朴素重放一致。
	replay, _ := New(8)
	naive := make([]int, 8)
	for _, p := range seq {
		if err := replay.Add(p[0], p[1]); err != nil {
			return err
		}
		if r := p[1] + 1; r > naive[p[0]] {
			naive[p[0]] = r
		}
	}
	got := replay.Registers()
	sum := 0
	for j := range got {
		if got[j] != naive[j] {
			return fmt.Errorf("selfcheck: replay mismatch at reg[%d]: %d != %d", j, got[j], naive[j])
		}
		sum += naive[j]
	}
	want := est.Alpha(8) * 8 * math.Exp2(float64(sum)/8)
	if got := replay.Estimate(); got != want {
		return fmt.Errorf("selfcheck: estimate %v != naive-formula %v", got, want)
	}

	// 不变量 4：失败不留痕，三类错误互不相同。
	for _, m := range []int{0, -4, 3, 6} {
		if _, err := New(m); !errors.Is(err, ErrInvalidM) {
			return fmt.Errorf("selfcheck: New(%d) err=%v", m, err)
		}
	}
	if ErrInvalidM == ErrBucketRange || ErrBucketRange == ErrNegativeZ || ErrInvalidM == ErrNegativeZ {
		return errors.New("selfcheck: sentinel errors not distinct")
	}
	before := replay.Registers() // replay 已注入 seq，直接复用其状态
	for _, c := range []struct {
		b, z int
		want error
	}{{-1, 0, ErrBucketRange}, {8, 0, ErrBucketRange}, {0, -1, ErrNegativeZ}} {
		if err := replay.Add(c.b, c.z); !errors.Is(err, c.want) {
			return fmt.Errorf("selfcheck: Add(%d,%d) err=%v", c.b, c.z, err)
		}
	}
	for j, v := range replay.Registers() {
		if v != before[j] {
			return fmt.Errorf("selfcheck: rejected op changed reg[%d]", j)
		}
	}
	return nil
}
