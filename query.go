package ontology

import (
	"math"
	"sort"
)

// snapshot 是唯一值升序排列后的只读视图，附带前缀权重和。
// 展开语义下，值 values[i] 占据展开下标区间 [cum[i]-w_i, cum[i])。
type snapshot struct {
	values []float64
	cum    []int64
	total  int64
}

// snapshot 拷贝当前状态并排序；只在查询时构造，不写回 Quantiler。
func (q *Quantiler) snapshot() snapshot {
	q.mu.RLock()
	defer q.mu.RUnlock()
	s := snapshot{
		values: make([]float64, 0, len(q.weights)),
		cum:    make([]int64, 0, len(q.weights)),
		total:  q.total,
	}
	for v := range q.weights {
		s.values = append(s.values, v)
	}
	sort.Float64s(s.values)
	var acc int64
	for _, v := range s.values {
		acc += q.weights[v]
		s.cum = append(s.cum, acc)
	}
	return s
}

// at 返回展开数组中 0 基下标 k 处的值。
func (s snapshot) at(k int64) float64 {
	i := sort.Search(len(s.cum), func(i int) bool { return s.cum[i] > k })
	return s.values[i]
}

// Quantile 按指定口径回答 Quantile(p)。
// p 越界或为 NaN 返回 ErrInvalidP；空数据集返回 ErrEmpty。
// 查询不修改内部状态，可并发调用。
func (q *Quantiler) Quantile(p float64, m Method) (float64, error) {
	if math.IsNaN(p) || p < 0 || p > 1 {
		return 0, ErrInvalidP
	}
	s := q.snapshot()
	if s.total == 0 {
		return 0, ErrEmpty
	}
	switch m {
	case NearestRank:
		return s.nearestRank(p), nil
	case Linear:
		return s.linear(p), nil
	}
	return 0, ErrUnknownMethod
}

// nearestRank：秩 r = ceil(p*N)（1 基），p=0 时取第一个样本。
func (s snapshot) nearestRank(p float64) float64 {
	k := int64(math.Ceil(p*float64(s.total))) - 1
	if k < 0 {
		k = 0
	}
	return s.at(k)
}

// linear 实现 R-7：小数秩 h = p*(N-1)（0 基），在相邻样本间线性插值。
// 浮点细节：
//   - frac == 0 时直接返回样本值，保证 p 落在样本点上时逐位等于该样本；
//   - 两端值相等（含同为 +Inf 或同为 -Inf）时直接返回该值，避免 Inf-Inf=NaN；
//   - 一端为无穷、另一端为有限值时返回该无穷（见包文档），不产生 NaN。
func (s snapshot) linear(p float64) float64 {
	h := p * float64(s.total-1)
	lo := int64(math.Floor(h))
	frac := h - float64(lo)
	a := s.at(lo)
	if frac == 0 {
		return a
	}
	b := s.at(lo + 1)
	if a == b {
		return a
	}
	if math.IsInf(a, 0) {
		return a
	}
	if math.IsInf(b, 0) {
		return b
	}
	return a + frac*(b-a)
}
