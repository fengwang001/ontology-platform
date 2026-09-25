// Package check 提供洗牌均匀性的统计工具。
package check

import (
	"errors"
	"math"
)

// ErrEmptyCounts 表示统计输入为空。
var ErrEmptyCounts = errors.New("check: empty counts")

// MaxMinRatio 返回计数最大值与最小值之比，越接近 1 分布越均匀。
func MaxMinRatio(counts []int64) (float64, error) {
	if len(counts) == 0 {
		return 0, ErrEmptyCounts
	}
	lo, hi := counts[0], counts[0]
	for _, c := range counts[1:] {
		lo = min(lo, c)
		hi = max(hi, c)
	}
	if lo == 0 {
		return math.Inf(1), nil
	}
	return float64(hi) / float64(lo), nil
}

// ChiSquare 返回各计数相对均匀期望的卡方统计量。
func ChiSquare(counts []int64) (float64, error) {
	if len(counts) == 0 {
		return 0, ErrEmptyCounts
	}
	var total int64
	for _, c := range counts {
		total += c
	}
	exp := float64(total) / float64(len(counts))
	var chi2 float64
	for _, c := range counts {
		d := float64(c) - exp
		chi2 += d * d / exp
	}
	return chi2, nil
}
