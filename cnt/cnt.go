// Package cnt 提供逆序对计数（归并排序变体，O(n log n)）。
package cnt

import "sync/atomic"

// comparisons 是非导出的元素比较次数计数器（进程内存，原子操作保证并发安全）。
var comparisons atomic.Int64

// Comparisons 返回累计的元素比较次数。
func Comparisons() int64 { return comparisons.Load() }

// ResetComparisons 清零比较计数器，供测试测量单次调用的上界。
func ResetComparisons() { comparisons.Store(0) }

// CountInversions 返回 arr 的逆序对数量。纯函数：不修改入参，无共享可写状态。
func CountInversions(arr []int) (int64, error) {
	if len(arr) < 2 {
		return 0, nil
	}
	a := make([]int, len(arr))
	copy(a, arr)
	buf := make([]int, len(arr))
	return sortCount(a, buf), nil
}

func sortCount(a, buf []int) int64 {
	if len(a) < 2 {
		return 0
	}
	mid := len(a) / 2
	total := sortCount(a[:mid], buf[:mid]) + sortCount(a[mid:], buf[mid:])
	return total + merge(a, buf, mid)
}

// merge 稳定归并 a[:mid] 与 a[mid:]，并累加逆序对贡献：
// 当左半剩余首元素大于右半当前元素时，左半剩余全部元素与之构成逆序对。
func merge(a, buf []int, mid int) int64 {
	var count int64
	i, j, k := 0, mid, 0
	for i < mid && j < len(a) {
		comparisons.Add(1)
		if a[i] <= a[j] { // 相等取左半，保证稳定
			buf[k] = a[i]
			i++
		} else {
			buf[k] = a[j]
			j++
			count += int64(mid - i) // 左半剩余数量
		}
		k++
	}
	k += copy(buf[k:], a[i:mid])
	k += copy(buf[k:], a[j:])
	copy(a, buf[:k])
	return count
}
