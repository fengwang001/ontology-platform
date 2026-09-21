// Package ontology 提供在线统计量累加器：流式接收 float64 样本，
// 随时读取计数、均值、总体方差与样本方差，并支持两个累加器的可交换合并。
//
// 数值稳定性：不采用「平方和减均值平方」的朴素两遍公式，而是使用
// Welford 递推（单趟、在线）：
//
//	n   ← n + 1
//	δ   ← x − mean
//	mean ← mean + δ/n
//	M2  ← M2 + δ·(x − mean)   // 注意此处用的是更新后的 mean
//
// 总体方差 = M2/n，样本方差 = M2/(n−1)。M2 始终是非负项之和，
// 读取方差时仍做一次下界钳制，保证绝不返回负数。
//
// 合并采用 Chan et al. 的并行公式，并在合并前对两个操作数做确定性排序，
// 因此 Merge(a, b) 与 Merge(b, a) 的结果逐位相同（见 merge.go）。
package ontology

import (
	"math"
	"sync"
)

// Accumulator 是在线统计量累加器，可安全地被多个协程并发使用。
// 零值即可用。
type Accumulator struct {
	mu       sync.Mutex
	n        int64   // 已接受样本数
	mean     float64 // 当前均值（Welford 递推）
	m2       float64 // 离均差平方和（Welford 递推）
	skipped  int64   // 被拒绝的 NaN 样本数
	poisoned bool    // 见过 ±Inf，统计量不可用
}

// Add 接收一个样本。
//
// NaN 被拒绝：跳过计数加一，统计量不受影响，返回 ErrNaNSample。
// ±Inf 被计入样本数，但会使统计量永久不可用（后续读取返回
// ErrStatsUnavailable），Add 本身返回 nil。
// +0.0 与 -0.0 都是合法样本，按数值 0 处理。
func (a *Accumulator) Add(x float64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if math.IsNaN(x) {
		a.skipped++
		return ErrNaNSample
	}
	if math.IsInf(x, 0) {
		a.n++
		a.poisoned = true
		return nil
	}
	a.n++
	delta := x - a.mean
	a.mean += delta / float64(a.n)
	a.m2 += delta * (x - a.mean)
	return nil
}

// Count 返回已接受的样本数（不含被拒绝的 NaN）。
func (a *Accumulator) Count() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.n
}

// Skipped 返回被拒绝的 NaN 样本数。
func (a *Accumulator) Skipped() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.skipped
}

// Mean 返回当前均值。零样本返回 ErrNoSamples；
// 统计量被 ±Inf 污染后返回 ErrStatsUnavailable。
func (a *Accumulator) Mean() (float64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.checkUsable(); err != nil {
		return 0, err
	}
	return a.mean, nil
}

// PopulationVariance 返回总体方差 M2/n。零样本返回 ErrNoSamples；
// 统计量被 ±Inf 污染后返回 ErrStatsUnavailable。返回值绝不小于 0。
func (a *Accumulator) PopulationVariance() (float64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.checkUsable(); err != nil {
		return 0, err
	}
	return clampNonNeg(a.m2 / float64(a.n)), nil
}

// SampleVariance 返回样本方差 M2/(n−1)。零样本返回 ErrNoSamples，
// 恰好一个样本返回 ErrInsufficientSamples（自由度为零），
// 统计量被 ±Inf 污染后返回 ErrStatsUnavailable。返回值绝不小于 0。
func (a *Accumulator) SampleVariance() (float64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.checkUsable(); err != nil {
		return 0, err
	}
	if a.n < 2 {
		return 0, ErrInsufficientSamples
	}
	return clampNonNeg(a.m2 / float64(a.n-1)), nil
}

// checkUsable 校验统计量是否可读。调用方必须已持有锁。
func (a *Accumulator) checkUsable() error {
	if a.poisoned {
		return ErrStatsUnavailable
	}
	if a.n == 0 {
		return ErrNoSamples
	}
	return nil
}

// snapshot 返回内部状态的一致性副本。调用方必须已持有锁。
func (a *Accumulator) snapshot() (n, skipped int64, mean, m2 float64, poisoned bool) {
	return a.n, a.skipped, a.mean, a.m2, a.poisoned
}

func clampNonNeg(v float64) float64 {
	if v < 0 {
		return 0
	}
	return v
}
