// Package shuffle 实现 Fisher-Yates 均匀洗牌。
package shuffle

import (
	"errors"
	"sync/atomic"

	"ontology/rng"
)

// ErrNilSlice 表示传入了 nil 切片（空切片合法，nil 不合法）。
var ErrNilSlice = errors.New("shuffle: 不能对 nil 切片洗牌")

// randCalls 是非导出计数器，记录随机数调用总次数（原子操作，race 干净）。
var randCalls atomic.Int64

// RandCalls 返回迄今为止的随机数调用总次数，供测试与 demo 断言。
func RandCalls() int64 {
	return randCalls.Load()
}

// Shuffle 原地对 arr 做 Fisher-Yates 洗牌，每种排列概率恰为 1/n!。
// 同 seed 洗同一数组结果逐元素相同；空切片与单元素原样返回。
// n 个元素恰好调用 n-1 次随机数。
func Shuffle[T any](arr []T, seed uint64) error {
	if arr == nil {
		return ErrNilSlice
	}
	s := rng.New(seed)
	for i := 0; i < len(arr)-1; i++ {
		j, err := s.Intn(len(arr) - i) // 第 i 步从 [i, n) 选，保证均匀
		if err != nil {
			return err
		}
		randCalls.Add(1)
		arr[i], arr[i+j] = arr[i+j], arr[i]
	}
	return nil
}
