package ontology

import (
	"math"
	"sync"
)

// Method 选择分位数口径。
type Method int

const (
	// NearestRank 最近秩法：结果一定是样本中真实存在的值。
	NearestRank Method = iota
	// Linear 线性插值法（R-7）：相邻样本间按小数秩线性插值。
	Linear
)

// Quantiler 是分位数查询器。只保存唯一值及其权重，不展开样本。
// 零值不可用，请用 New 构造。并发查询安全；写入与查询互斥。
type Quantiler struct {
	mu      sync.RWMutex
	weights map[float64]int64 // 唯一值 -> 权重（正整数）
	total   int64             // 总权重
	skipped int64             // 被拒绝的 NaN 样本数
}

// New 返回一个空的分位数查询器。
func New() *Quantiler {
	return &Quantiler{weights: make(map[float64]int64)}
}

// Add 加入一个权重为 1 的样本。
func (q *Quantiler) Add(v float64) error {
	return q.AddWeighted(v, 1)
}

// AddWeighted 加入一个权重为 w 的样本，语义等价于该值重复出现 w 次。
// w 必须是正整数；v 为 NaN 时被拒绝并计入 SkippedNaN。
// +0.0 与 -0.0 视为同一个值，内部统一存为 +0.0。
func (q *Quantiler) AddWeighted(v, w float64) error {
	if math.IsNaN(v) {
		q.mu.Lock()
		q.skipped++
		q.mu.Unlock()
		return ErrNaNSample
	}
	if w < 1 || w != math.Trunc(w) || w >= 1<<63 {
		return ErrInvalidWeight
	}
	if v == 0 {
		v = 0 // 把 -0.0 归一化为 +0.0
	}
	q.mu.Lock()
	q.weights[v] += int64(w)
	q.total += int64(w)
	q.mu.Unlock()
	return nil
}

// UniqueCount 返回当前唯一值个数。
func (q *Quantiler) UniqueCount() int {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return len(q.weights)
}

// TotalWeight 返回总权重（即展开语义下的样本总数）。
func (q *Quantiler) TotalWeight() int64 {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return q.total
}

// SkippedNaN 返回被拒绝加入的 NaN 样本个数。
func (q *Quantiler) SkippedNaN() int64 {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return q.skipped
}
