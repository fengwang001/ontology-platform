package ontology

import "math"

// 桶下标结果约定：
const (
	idxNaN   = -2 // 样本为 NaN
	idxUnder = -1 // 样本落在 [lo, hi) 之下（含 -Inf）
)

// NaiveIndex 是“天真”算法 int((x-lo)/w) 的结果，仅用于对照展示浮点舍入问题。
// 调用方需保证 x 为有限数且落在 [lo, hi) 内。
func NaiveIndex(lo, width float64, n int, x float64) int {
	i := int((x - lo) / width)
	if i < 0 {
		return 0
	}
	if i >= n {
		return n - 1
	}
	return i
}

// lowerEdge 返回第 i 个桶的左边界 lo+i*w。
// 最后一个桶的右端不由此推算，而是精确使用 hi（见 indexFor）。
func (h *Histogram) lowerEdge(i int) float64 {
	return h.lo + float64(i)*h.w
}

// indexFor 返回样本 x 的归属桶号；x < lo 返回 -1，x >= hi 返回 n，
// NaN 返回 idxNaN。桶号在算出后用真实桶边界逐侧校正，保证边界样本归属
// 不含糊：每个桶为 [edge_i, edge_{i+1})，最后一个桶右端精确为 hi。
func (h *Histogram) indexFor(x float64) int {
	if math.IsNaN(x) {
		return idxNaN
	}
	if x < h.lo {
		return idxUnder
	}
	if x >= h.hi {
		return h.n // 上溢：hi 本身也不进最后一桶
	}

	i := int((x - h.lo) / h.w)
	if i < 0 {
		i = 0
	}
	if i >= h.n {
		i = h.n - 1
	}

	// 若 x 实际不在当前桶区间内，沿边界逐桶修正。
	for i > 0 && x < h.lowerEdge(i) {
		i--
	}
	for i < h.n-1 && x >= h.lowerEdge(i+1) {
		i++
	}
	return i
}
