// Package shuffle 实现均匀的 Fisher-Yates 洗牌。
package shuffle

import (
	"ontology/rng"
)

// Shuffle 原地执行 Fisher-Yates 洗牌（从后往前，第 i 步在 [0, i] 中选 j）。
//
// 同 seed、同输入逐元素可复现；每次调用都新建随机源，不共享状态，
// 因此对不同切片并发调用互不影响。返回值 calls 是随机数调用次数：
// n 个元素恰好 n-1 次（空切片与单元素为 0）。
func Shuffle[T any](arr []T, seed uint64) (calls int, err error) {
	source := rng.New(seed)
	for i := len(arr) - 1; i > 0; i-- {
		j, e := source.Intn(i + 1) // 只从未确定位置的范围 [0, i] 里选
		if e != nil {
			return source.Calls(), e
		}
		arr[i], arr[j] = arr[j], arr[i]
	}
	return source.Calls(), nil
}

// IsPermutation 报告 got 是否是 want 的多集置换（元素集合不变，仅顺序可变）。
func IsPermutation[T comparable](got, want []T) bool {
	if len(got) != len(want) {
		return false
	}
	counts := make(map[T]int, len(want))
	for _, v := range want {
		counts[v]++
	}
	for _, v := range got {
		counts[v]--
		if counts[v] < 0 {
			return false
		}
	}
	return true
}
