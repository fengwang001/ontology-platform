// Package check 提供洗牌均匀性的统计判定工具。
package check

import (
	"errors"
	"fmt"
	"sort"
)

var (
	// ErrBadTrials 表示采样参数非法（n <= 0 或 trials <= 0）。
	ErrBadTrials = errors.New("check: n and trials must be positive")
	// ErrNoCounts 表示计数表为空，无法统计。
	ErrNoCounts = errors.New("check: no permutation counts")
)

// ShuffleFunc 是可统计的洗牌函数签名，与 shuffle.Shuffle[int] 一致。
type ShuffleFunc func(arr []int, seed uint64)

// Distribution 用 shuf 对 [0..n) 洗 trials 次（种子取 0..trials-1），
// 返回每种排列的出现次数。
func Distribution(n, trials int, shuf ShuffleFunc) (map[string]int, error) {
	if n <= 0 || trials <= 0 {
		return nil, ErrBadTrials
	}
	counts := make(map[string]int)
	for t := 0; t < trials; t++ {
		arr := make([]int, n)
		for i := range arr {
			arr[i] = i
		}
		shuf(arr, uint64(t))
		counts[fmt.Sprint(arr)]++
	}
	return counts, nil
}

// MaxMinRatio 返回计数表中最大值与最小值之比，越接近 1 越均匀。
func MaxMinRatio(counts map[string]int) (float64, error) {
	if len(counts) == 0 {
		return 0, ErrNoCounts
	}
	lo, hi := 0, 0
	first := true
	for _, c := range counts {
		if first || c < lo {
			lo = c
		}
		if first || c > hi {
			hi = c
		}
		first = false
	}
	return float64(hi) / float64(lo), nil
}

// ChiSquare 返回计数表相对均匀分布的卡方统计量。
func ChiSquare(counts map[string]int) (float64, error) {
	if len(counts) == 0 {
		return 0, ErrNoCounts
	}
	total := 0
	for _, c := range counts {
		total += c
	}
	expect := float64(total) / float64(len(counts))
	chi := 0.0
	for _, c := range counts {
		d := float64(c) - expect
		chi += d * d / expect
	}
	return chi, nil
}

// IsPermutation 报告 b 是否为 a 的多集置换（元素集合不变）。
func IsPermutation(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	x, y := append([]int(nil), a...), append([]int(nil), b...)
	sort.Ints(x)
	sort.Ints(y)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}
