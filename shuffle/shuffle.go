// Package shuffle 实现 Fisher-Yates 均匀洗牌。
package shuffle

import (
	"sync/atomic"

	"ontology/rng"
)

// randCalls 统计全进程随机数调用总次数（非导出，测试经 RandCalls 读增量）。
var randCalls atomic.Int64

// RandCalls 返回至今的随机数调用总次数，用于校验调用次数不变量。
func RandCalls() int64 {
	return randCalls.Load()
}

// Shuffle 原地均匀打乱 arr：每种排列概率恰为 1/n!。
// 同 seed 结果确定可复现；n 个元素恰好调用 n-1 次随机数。
// 空切片与单元素切片合法，原样返回。每次调用使用独立随机源，
// 并发打乱不同切片互不影响。
func Shuffle[T any](arr []T, seed uint64) {
	n := len(arr)
	if n < 2 {
		return
	}
	src := rng.New(seed)
	for i := 0; i < n-1; i++ {
		j, err := src.Intn(n - i) // 从未洗部分 [i, n) 选
		if err != nil {
			return // 不可达：n-i >= 2
		}
		randCalls.Add(1)
		arr[i], arr[i+j] = arr[i+j], arr[i]
	}
}
