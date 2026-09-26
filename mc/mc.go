// Package mc 实现蒙特卡洛积分的采样累加：Add 累加 f(x) 并计数，Estimate 给出估计。
// 内存 O(1)：只保留 sum 与 n 两个标量，绝不保留历史采样点。
package mc

import (
	"errors"
	"sync"

	"ontology/fn"
)

// 可判定哨兵错误，互不相同。
var (
	// ErrOutOfRange 表示采样点越界（x < a 或 x > b）。
	ErrOutOfRange = errors.New("mc: sample out of range")
	// ErrNoSamples 表示 n=0 时查询估计。
	ErrNoSamples = errors.New("mc: no samples")
)

// Accumulator 是采样累加器。并发安全。
type Accumulator struct {
	f    fn.Func
	a, b float64

	mu  sync.RWMutex
	sum float64
	n   int64

	// retained 记录为求平均而保留的历史采样点个数，恒为 0（O(1) 内存的证据）。
	// 非导出，不出现在任何公开接口。
	retained int
}

// New 构造累加器。调用方须保证 a <= b 且 f 非零值。
func New(f fn.Func, a, b float64) *Accumulator {
	return &Accumulator{f: f, a: a, b: b}
}

// Add 喂入一个采样点 x（须满足 a <= x <= b），累加 f(x) 并计数。
// 越界时返回 ErrOutOfRange，且不改变任何状态。
func (ac *Accumulator) Add(x float64) error {
	if x < ac.a || x > ac.b {
		return ErrOutOfRange
	}
	fx := ac.f.At(x)
	ac.mu.Lock()
	ac.sum += fx
	ac.n++
	ac.mu.Unlock()
	return nil
}

// Estimate 返回 (b-a)*sum/n；n=0 时返回 ErrNoSamples，状态不变。
func (ac *Accumulator) Estimate() (float64, error) {
	ac.mu.RLock()
	defer ac.mu.RUnlock()
	if ac.n == 0 {
		return 0, ErrNoSamples
	}
	return (ac.b - ac.a) * ac.sum / float64(ac.n), nil
}

// Samples 返回已接受的采样点个数 n。
func (ac *Accumulator) Samples() int64 {
	ac.mu.RLock()
	defer ac.mu.RUnlock()
	return ac.n
}
