// Package ontology 提供在线统计量累加器：流式接收 float64 样本，
// 随时读取计数、均值、总体方差与样本方差，并支持可交换的合并。
//
// 数值稳定性：不采用「平方和减均值平方」的朴素两遍公式，而是使用
// Welford 递推（单趟、在线）：
//
//	delta = x - mean
//	mean += delta / n
//	m2   += delta * (x - mean)   // 此时 mean 已是更新后的值
//
// 其中 m2 是「离均差平方和」，总体方差 = m2/n，样本方差 = m2/(n-1)。
// m2 数学上非负；为防御浮点舍入，读取方差时钳制到 0，绝不返回负值。
package ontology

import (
	"math"
	"sync"
)

// Accumulator 是在线统计量累加器，可安全地被多协程并发使用。
// 零值即可用。
type Accumulator struct {
	mu      sync.Mutex
	count   int64   // 已接受的有限样本数（含 ±Inf 样本）
	mean    float64 // 当前均值（Welford 递推）
	m2      float64 // 离均差平方和
	skipped int64   // 被拒绝的 NaN 样本数
	broken  bool    // 是否已混入 ±Inf，统计量不可用
}

// Add 接收一个样本。NaN 被拒绝并计入跳过计数；±Inf 会使累加器进入
// 「统计量不可用」状态（仍计入样本数）。+0.0 与 -0.0 均按数值 0 处理。
func (a *Accumulator) Add(x float64) {
	if math.IsNaN(x) {
		a.mu.Lock()
		a.skipped++
		a.mu.Unlock()
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.count++
	if math.IsInf(x, 0) {
		a.broken = true
		return
	}
	if a.broken {
		return
	}
	n := float64(a.count)
	delta := x - a.mean
	a.mean += delta / n
	a.m2 += delta * (x - a.mean)
}

// Count 返回已接受的样本数（含 ±Inf，不含被拒绝的 NaN）。
func (a *Accumulator) Count() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.count
}

// Skipped 返回被拒绝的 NaN 样本数。
func (a *Accumulator) Skipped() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.skipped
}

// Mean 返回当前均值。零样本返回 ErrNoSamples；统计量被 ±Inf 污染时
// 返回 ErrUnavailable。
func (a *Accumulator) Mean() (float64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.broken {
		return 0, ErrUnavailable
	}
	if a.count == 0 {
		return 0, ErrNoSamples
	}
	return a.mean, nil
}

// Variance 返回总体方差（除以 n）。零样本返回 ErrNoSamples；
// 统计量不可用时返回 ErrUnavailable。结果保证非负。
func (a *Accumulator) Variance() (float64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.broken {
		return 0, ErrUnavailable
	}
	if a.count == 0 {
		return 0, ErrNoSamples
	}
	return clampNonNeg(a.m2 / float64(a.count)), nil
}

// SampleVariance 返回样本方差（除以 n-1）。零样本返回 ErrNoSamples；
// 单样本自由度为零，返回 ErrTooFewSamples；统计量不可用时返回
// ErrUnavailable。结果保证非负。
func (a *Accumulator) SampleVariance() (float64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.broken {
		return 0, ErrUnavailable
	}
	if a.count == 0 {
		return 0, ErrNoSamples
	}
	if a.count == 1 {
		return 0, ErrTooFewSamples
	}
	return clampNonNeg(a.m2 / float64(a.count-1)), nil
}

func clampNonNeg(v float64) float64 {
	if v < 0 {
		return 0
	}
	return v
}

// state 是 Accumulator 内部状态的不含锁快照。
type state struct {
	count   int64
	mean    float64
	m2      float64
	skipped int64
	broken  bool
}

// snapshot 在锁内拷贝内部状态，供 Merge 使用且不修改源。
func (a *Accumulator) snapshot() state {
	a.mu.Lock()
	defer a.mu.Unlock()
	return state{
		count:   a.count,
		mean:    a.mean,
		m2:      a.m2,
		skipped: a.skipped,
		broken:  a.broken,
	}
}
