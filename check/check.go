// Package check 提供洗牌均匀性的统计检验。
package check

import (
	"errors"
	"fmt"
	"math"

	"ontology/rng"
)

var (
	// ErrEmptyInput 在输入没有元素时返回。
	ErrEmptyInput = errors.New("check: empty input distribution")
	// ErrNonUniform 在最大/最小频次比超过 maxRatio 时返回。
	ErrNonUniform = errors.New("check: distribution is not uniform")
)

// Distribution 对 n 个元素洗 trials 次（seed 取 1..trials，保证可复现），
// 返回每种排列的出现次数，键是排列的字符串形式。
func Distribution(n, trials int, fn func([]int, uint64) (int, error)) (map[string]int, error) {
	if n <= 0 || trials <= 0 {
		return nil, ErrEmptyInput
	}
	base := make([]int, n)
	for i := range base {
		base[i] = i
	}
	dist := make(map[string]int)
	for s := uint64(1); s <= uint64(trials); s++ {
		arr := append([]int(nil), base...)
		if _, err := fn(arr, s); err != nil {
			return nil, err
		}
		dist[fmt.Sprint(arr)]++
	}
	return dist, nil
}

// Ratio 返回频次分布中最大与最小出现次数之比；min 为 0 时返回 +Inf。
func Ratio(dist map[string]int) float64 {
	if len(dist) == 0 {
		return 0
	}
	max, min := 0, -1
	for _, c := range dist {
		if c > max {
			max = c
		}
		if min < 0 || c < min {
			min = c
		}
	}
	if min == 0 {
		return math.Inf(1)
	}
	return float64(max) / float64(min)
}

// Verify 检验分布的最大/最小频次比不超过 maxRatio，否则返回 ErrNonUniform。
func Verify(dist map[string]int, maxRatio float64) error {
	if r := Ratio(dist); r > maxRatio {
		return fmt.Errorf("%w: ratio=%.2f", ErrNonUniform, r)
	}
	return nil
}

// BuggyFullRangePick 是线上事故的错误实现：每一步都在整个数组 [0, n) 里选下标。
// 共产生 n^n 条等概率路径，而排列只有 n! 种，n! 不整除 n^n，故分布严重不均匀。
func BuggyFullRangePick(arr []int, seed uint64) (int, error) {
	source := rng.New(seed)
	for i := len(arr) - 1; i > 0; i-- {
		j, err := source.Intn(len(arr)) // 错误：范围应是 i+1 而不是 len(arr)
		if err != nil {
			return source.Calls(), err
		}
		arr[i], arr[j] = arr[j], arr[i]
	}
	return source.Calls(), nil
}
