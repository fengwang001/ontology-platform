// Package check 提供朴素数组参照实现，用于对拍 Fenwick 树。
package check

// Naive 直接对数组累加，单次查询 O(n)，仅作正确性参照。
type Naive struct{ a []int }

func NewNaive(n int) *Naive { return &Naive{a: make([]int, n)} }

func (v *Naive) Add(i, delta int) { v.a[i] += delta }

// PrefixSum 返回下标 0..i 的和；i == -1 时循环不执行，返回 0。
func (v *Naive) PrefixSum(i int) int {
	sum := 0
	for j := 0; j <= i; j++ {
		sum += v.a[j]
	}
	return sum
}

// RangeSum 返回闭区间 [l, r] 的和。
func (v *Naive) RangeSum(l, r int) int { return v.PrefixSum(r) - v.PrefixSum(l-1) }
