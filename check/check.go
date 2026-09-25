// Package check 提供洗牌结果的均匀性与置换性统计校验。
package check

import (
	"errors"
	"fmt"
)

// 哨兵错误，可用 errors.Is 区分。
var (
	// ErrNotPermutation 表示洗牌结果不是原数组的多集置换。
	ErrNotPermutation = errors.New("check: result is not a permutation")
	// ErrNonUniform 表示排列次数分布超出均匀性阈值。
	ErrNonUniform = errors.New("check: distribution is not uniform")
)

// VerifyPermutation 校验 after 是 before 的多集置换（元素集合不变）。
func VerifyPermutation[T comparable](before, after []T) error {
	if len(before) != len(after) {
		return fmt.Errorf("len %d != %d: %w", len(after), len(before), ErrNotPermutation)
	}
	count := make(map[T]int, len(before))
	for _, v := range before {
		count[v]++
	}
	for _, v := range after {
		count[v]--
		if count[v] < 0 {
			return fmt.Errorf("element %v lost or duplicated: %w", v, ErrNotPermutation)
		}
	}
	return nil
}

// MaxMinRatio 返回次数分布的最大/最小比；min 为 0 时返回 +Inf。
func MaxMinRatio(counts []int) float64 {
	lo, hi := 0, 0
	for i, c := range counts {
		if i == 0 || c < lo {
			lo = c
		}
		if c > hi {
			hi = c
		}
	}
	if lo == 0 {
		if hi == 0 {
			return 1
		}
		return 1e308
	}
	return float64(hi) / float64(lo)
}

// VerifyUniform 校验次数分布的最大/最小比不超过 maxRatio。
func VerifyUniform(counts []int, maxRatio float64) error {
	if r := MaxMinRatio(counts); r > maxRatio {
		return fmt.Errorf("max/min ratio %.3f > %.3f: %w", r, maxRatio, ErrNonUniform)
	}
	return nil
}
