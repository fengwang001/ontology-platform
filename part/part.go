// Package part 实现三路分区（荷兰国旗问题）：把切片原地重排为
// [<pivot | ==pivot | >pivot] 三段，返回等于段的起止下标。
package part

import (
	"cmp"
	"sync/atomic"
)

// swaps 非导出交换计数器，用于证明复杂度上界；atomic 保证并发调用 -race 干净。
var swaps atomic.Int64

// Swaps 返回进程内累计交换次数（仅供测试/观测断言上界）。
func Swaps() int64 { return swaps.Load() }

// ThreeWayPartition 原地重排 arr 为三段，返回等于段区间 [lt, gt)。
// 不变量：[0,lt)<pivot，[lt,i)==pivot，[gt,len) >pivot，[i,gt) 未扫描。
// 每轮 gt-i 严格减 1，O(n) 时间、O(1) 额外空间；空切片返回 (0,0)。
func ThreeWayPartition[T cmp.Ordered](arr []T, pivot T) (lt, gt int) {
	i, gt := 0, len(arr)
	for i < gt {
		switch {
		case arr[i] < pivot:
			arr[lt], arr[i] = arr[i], arr[lt]
			swaps.Add(1)
			lt++
			i++
		case arr[i] > pivot:
			gt--
			arr[i], arr[gt] = arr[gt], arr[i]
			swaps.Add(1)
			// i 不前移：换到 i 处的元素未分类，下轮再判。
		default: // arr[i] == pivot
			i++
		}
	}
	return lt, gt
}
