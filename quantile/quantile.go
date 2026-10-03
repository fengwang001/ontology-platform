// Package quantile 在直方图上按固定秩与插值规则估计分位数。
package quantile

import (
	"math/bits"

	"ontology/schema"
)

// Quantile 估计 h 的 q 千分位（q 为 0 到 1000 的整数）。
// 秩 r = max(1, ceil(q*N/1000))，取累计计数首次不小于 r 的桶 i；
// 桶内插值 lo + floor((hi-lo)*w/c)，乘积用 128 位精确计算。
// 返回值落在溢出桶时返回最后一个边界并置 Saturated 为 true。
// 错误顺序：参数非法、空。
func Quantile(h *schema.Hist, q int64) (value int64, saturated bool, err error) {
	if q < 0 || q > 1000 {
		return 0, false, schema.ErrInvalid
	}
	if err := h.Validate(); err != nil {
		return 0, false, err
	}
	var n int64
	for _, c := range h.Counts {
		n += c // Validate 已保证总和不超过 int64
	}
	if n == 0 {
		return 0, false, schema.ErrEmpty
	}
	r := rank(q, n)
	var cum int64
	last := len(h.Bounds)
	for i, c := range h.Counts {
		if cum+c >= r {
			if i == last {
				return h.Bounds[last-1], true, nil // 溢出桶：不外推
			}
			lo := int64(0)
			if i > 0 {
				lo = h.Bounds[i-1]
			}
			hi := h.Bounds[i]
			w := r - cum
			return lo + mulDiv64(uint64(hi-lo), uint64(w), uint64(c)), false, nil
		}
		cum += c
	}
	panic("unreachable: 累计计数必在某桶达到 r")
}

// rank 计算 max(1, ceil(q*n/1000))，乘积用 128 位避免溢出。
func rank(q, n int64) int64 {
	hi, lo := bits.Mul64(uint64(q), uint64(n))
	quo, rem := bits.Div64(hi, lo, 1000)
	if rem != 0 {
		quo++
	}
	if quo < 1 {
		quo = 1
	}
	return int64(quo)
}

// mulDiv64 返回 floor(a*b/c)，乘积按 128 位精确计算。
// 调用处保证结果不超过 math.MaxInt64（故 a*b 的高 64 位小于 c）。
func mulDiv64(a, b, c uint64) int64 {
	hi, lo := bits.Mul64(a, b)
	quo, _ := bits.Div64(hi, lo, c)
	return int64(quo)
}
