package ontology

import (
	"math"
	"sync"
)

// Accumulator 是在线统计量累加器，并发安全。
// 内部状态为 Welford 递推的 (count, mean, m2)，
// 读取方差时由 m2 派生，绝不保存平方和。
type Accumulator struct {
	mu       sync.RWMutex
	count    uint64
	mean     float64
	m2       float64
	skipped  uint64
	unusable bool
}

// New 返回一个空的累加器。
func New() *Accumulator {
	return &Accumulator{}
}

// Add 喂入一个样本。
//
// NaN 样本被拒绝：不更新任何统计量，跳过计数加一，返回 ErrNaN。
// 正负 Inf 样本计入计数，但会使统计量进入不可用状态，此后
// Mean/Variance/SampleVariance 均返回 ErrUnavailable。
// +0.0 与 -0.0 都是合法样本，按数值 0 处理。
func (a *Accumulator) Add(x float64) error {
	if math.IsNaN(x) {
		a.mu.Lock()
		a.skipped++
		a.mu.Unlock()
		return ErrNaN
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if math.IsInf(x, 0) {
		a.count++
		a.unusable = true
		return nil
	}
	if a.unusable {
		a.count++
		return nil
	}
	a.count++
	d1 := x - a.mean
	a.mean += d1 / float64(a.count)
	d2 := x - a.mean
	a.m2 += d1 * d2
	return nil
}

// Count 返回当前已接受的样本数（不含被拒绝的 NaN）。
func (a *Accumulator) Count() uint64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.count
}

// Skipped 返回被拒绝的 NaN 样本数。
func (a *Accumulator) Skipped() uint64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.skipped
}

// snapshot 在持锁状态下返回内部状态副本，供统计读取与合并使用。
func (a *Accumulator) snapshot() (count uint64, mean, m2 float64, skipped uint64, unusable bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.count, a.mean, a.m2, a.skipped, a.unusable
}
