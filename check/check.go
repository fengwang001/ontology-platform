// Package check 提供洗牌均匀性的统计检验。
package check

import (
	"errors"
	"fmt"

	"ontology/shuffle"
)

// ErrNonUniform 表示排列计数分布超出均匀性阈值。
var ErrNonUniform = errors.New("check: 排列分布不均匀")

// CountPermutations 对 [0..n) 洗牌 trials 次（第 i 次种子为 seed+i），
// 返回每种排列的出现次数。
func CountPermutations(n, trials int, seed uint64) (map[string]int, error) {
	counts := make(map[string]int)
	for i := 0; i < trials; i++ {
		arr := make([]int, n)
		for j := range arr {
			arr[j] = j
		}
		if err := shuffle.Shuffle(arr, seed+uint64(i)); err != nil {
			return nil, err
		}
		counts[fmt.Sprint(arr)]++
	}
	return counts, nil
}

// MaxMinRatio 返回计数最大值与最小值之比；counts 为空时返回 1。
func MaxMinRatio(counts map[string]int) float64 {
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
	if lo == 0 {
		return float64(hi) + 1 // 某排列出现 0 次，视为极不均匀
	}
	return float64(hi) / float64(lo)
}

// VerifyUniform 检验 n 元素洗 trials 次后，排列计数比不超过 maxRatio。
func VerifyUniform(n, trials int, seed uint64, maxRatio float64) error {
	counts, err := CountPermutations(n, trials, seed)
	if err != nil {
		return err
	}
	if r := MaxMinRatio(counts); r > maxRatio {
		return fmt.Errorf("%w: max/min=%.2f > %.2f", ErrNonUniform, r, maxRatio)
	}
	return nil
}
