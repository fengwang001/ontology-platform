// Package shuffle 实现 Fisher-Yates 均匀洗牌。
package shuffle

import (
	"sync/atomic"

	"ontology/rng"
)

// randomCalls 是非导出的全局计数器：累计 Shuffle 调用 rng 的次数。
// 仅用原子操作访问，并发安全；测试可借此断言 n 个元素恰好 n-1 次。
var randomCalls atomic.Int64

// RandomCalls 返回累计的随机数调用次数。
func RandomCalls() int64 {
	return randomCalls.Load()
}

// ResetRandomCalls 将随机数调用计数清零（供测试与 demo 使用）。
func ResetRandomCalls() {
	randomCalls.Store(0)
}

// Shuffle 原地对 arr 做 Fisher-Yates 洗牌：第 i 步从 [i, n) 中等概率
// 选 j 并与位置 i 交换，保证每种排列恰好以 1/n! 的概率出现。
// 同 seed 洗同一数组结果逐元素相同；空数组与单元素原样返回。
// 每次调用使用独立的 rng 实例，并发洗不同数组互不影响。
func Shuffle[T any](arr []T, seed uint64) {
	n := len(arr)
	if n < 2 {
		return
	}
	r := rng.New(seed)
	for i := 0; i < n-1; i++ {
		j, err := r.Intn(n - i)
		if err != nil {
			panic(err) // n-i >= 2，不可达
		}
		j += i
		arr[i], arr[j] = arr[j], arr[i]
		randomCalls.Add(1)
	}
}
