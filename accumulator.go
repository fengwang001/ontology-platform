// Package ontology 提供在线统计量累加器：流式接收 float64 样本，
// 随时读出计数、均值、总体方差与样本方差，并支持可交换、可合并的并行聚合。
//
// 数值稳定性：不采用「平方和减均值平方」的朴素两遍公式，而是使用
// Welford 在线递推（见 Add）与 Chan 等人的并行合并公式（见 Merge），
// 对 1e9 量级偏移的小方差样本仍保持 1e-12 级相对精度。
package ontology

import (
	"math"
	"sync"
)

// Accumulator 是在线统计量累加器，并发安全，状态全部在进程内存中。
//
// 内部维护 Welford 递推的三个状态量：
//   - count：样本数
//   - mean：  当前均值
//   - m2：    离差平方和（sum of squared deviations），总体方差 = m2/count
//
// 另维护 skipped（被拒绝的 NaN 样本数）与 broken（统计量不可用标记）。
type Accumulator struct {
	mu      sync.Mutex
	count   uint64
	mean    float64
	m2      float64
	skipped uint64
	broken  bool
}

// New 返回一个空的累加器。
func New() *Accumulator {
	return &Accumulator{}
}

// Add 喂入一个样本。
//
// NaN 样本被拒绝：不计入统计量，只增加跳过计数，并返回 ErrNaNRejected。
// 正负无穷是合法输入，但会使后续统计量变为非有限值；此后所有统计量
// 读取都返回 ErrUnusable（累加器本身仍继续计数）。
// +0.0 与 -0.0 都是合法样本，按数值 0 处理。
//
// 递推公式（Welford, 1962）：
//
//	n     = n + 1
//	delta = x - mean
//	mean  = mean + delta/n
//	m2    = m2 + delta*(x - mean)
func (a *Accumulator) Add(x float64) error {
	if math.IsNaN(x) {
		a.mu.Lock()
		a.skipped++
		a.mu.Unlock()
		return ErrNaNRejected
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	a.count++
	delta := x - a.mean
	a.mean += delta / float64(a.count)
	a.m2 += delta * (x - a.mean)
	// m2 数学上恒非负；浮点舍入可能给出微小负值，钳到 0，
	// 保证方差绝不出现负数（全等样本时 m2 精确保持 0）。
	if a.m2 < 0 {
		a.m2 = 0
	}
	if math.IsInf(x, 0) || math.IsNaN(a.mean) || math.IsInf(a.mean, 0) ||
		math.IsNaN(a.m2) || math.IsInf(a.m2, 0) {
		a.broken = true
	}
	return nil
}

// snapshot 在锁内拷贝全部状态，供 Merge 与读取方法使用，
// 保证不会观察到半更新状态。
func (a *Accumulator) snapshot() Accumulator {
	a.mu.Lock()
	defer a.mu.Unlock()
	return Accumulator{
		count:   a.count,
		mean:    a.mean,
		m2:      a.m2,
		skipped: a.skipped,
		broken:  a.broken,
	}
}
