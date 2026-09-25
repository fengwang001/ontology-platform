// Package shuffle 实现 Fisher-Yates 均匀洗牌。
package shuffle

import (
	"errors"
	"sync/atomic"

	"ontology/rng"
)

// ErrNilSlice 表示传入了 nil 切片（空但非 nil 的切片是合法的）。
var ErrNilSlice = errors.New("shuffle: nil slice")

// randCalls 统计最近一次 Shuffle 的随机数调用次数（非导出计数器）。
var randCalls atomic.Int64

// RandCalls 返回最近一次 Shuffle 调用随机源的次数，供测试与演示断言。
func RandCalls() int64 {
	return randCalls.Load()
}

// Shuffle 原地打乱 arr：第 i 步在 [0, i] 中等概率选 j 与 a[i] 交换，
// 保证每种排列等概率（1/n!）。同 seed 结果可复现；空/单元素原样返回。
// 每次调用使用独立随机源，并发洗不同数组互不影响。
func Shuffle[T any](arr []T, seed uint64) error {
	if arr == nil {
		return ErrNilSlice
	}
	randCalls.Store(0)
	g := rng.New(seed)
	for i := len(arr) - 1; i > 0; i-- {
		j, err := g.Intn(i + 1)
		if err != nil {
			return err
		}
		arr[i], arr[j] = arr[j], arr[i]
		randCalls.Add(1)
	}
	return nil
}
