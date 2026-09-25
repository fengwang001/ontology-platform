// Package check 提供朴素参照实现（计数后重排），用于对照验证 part 包。
package check

import (
	"cmp"
	"slices"
)

// Naive 朴素三路分区：先扫描计数三段长度，再从副本按段回填。
// 使用 O(n) 额外空间，仅作测试参照；返回等于段区间 [lt, gt)。
func Naive[T cmp.Ordered](arr []T, pivot T) (lt, gt int) {
	src := slices.Clone(arr)
	var nLT, nEQ int
	for _, v := range src {
		switch {
		case v < pivot:
			nLT++
		case v == pivot:
			nEQ++
		}
	}
	iLT, iEQ, iGT := 0, nLT, nLT+nEQ
	for _, v := range src {
		switch {
		case v < pivot:
			arr[iLT] = v
			iLT++
		case v == pivot:
			arr[iEQ] = v
			iEQ++
		default:
			arr[iGT] = v
			iGT++
		}
	}
	return nLT, nLT + nEQ
}
