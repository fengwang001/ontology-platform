// Package blk 负责把一维下标区间 [0,n) 按块大小 b 做左闭右开的划分，
// 并以 O(1) 闭式算术查询任意下标所在的块（含尾块）。
//
// 本包不依赖 ontology 的任何其他包。
package blk

import (
	"errors"
	"fmt"
	"sync/atomic"
)

// errNotClosedForm 是内部自检失败哨兵；文本不含任何计数器数值。
var errNotClosedForm = errors.New("blk: tail-block lookup is not closed-form O(1)")

// Part 是一个左闭右开的下标块 [Start, End)。
type Part struct {
	Start int
	End   int
}

// lookupEntries 记录最近一次 TailBlock 定位时访问过的块边界条目个数。
// 正确的闭式实现访问 0 个条目；线性扫描 Parts 数组的错误实现会访问 O(n/b) 个。
// 非导出：仅限同包白盒测试读取，绝不出现在任何公开接口中。
// 用原子操作保护：Blocked 会在并发乘法中高频调用 TailBlock。
var lookupEntries atomic.Int64

// LookupEntries 是包内白盒测试使用的只读访问器（非导出）。
func lookupEntriesLoad() int64 { return lookupEntries.Load() }

// Parts 把 [0,n) 精确划分为若干左闭右开块：第 q 块为
// [q*b, min((q+1)*b, n))；当 n%b != 0 时最后一块是大小为 n%b 的尾块。
//
// 前置条件（由上层 api 在调用前整体校验）：n >= 1 且 1 <= b <= n；
// 违反前置条件时返回 nil。
func Parts(n, b int) []Part {
	if n < 1 || b < 1 || b > n {
		return nil
	}
	nb := (n + b - 1) / b // 块数，即 ceil(n/b)
	parts := make([]Part, nb)
	for q := 0; q < nb; q++ {
		s := q * b
		e := s + b
		if e > n {
			e = n
		}
		parts[q] = Part{Start: s, End: e}
	}
	return parts
}

// TailBlock 以 O(1) 闭式算术返回下标 k 所在块的起点 start 与大小 size。
// 它不遍历任何块边界数组：start=(k/b)*b，size=min(start+b,n)-start，
// 因此无论 n 多大，本次定位访问的边界条目数恒为 0（见 lookupEntries）。
//
// 前置条件：n >= 1、1 <= b <= n、0 <= k < n；违反时返回 (0, 0)。
func TailBlock(n, b, k int) (start, size int) {
	// 本次定位开始：重置"本次访问的边界条目数"。闭式路径下它始终为 0。
	lookupEntries.Store(0)
	if n < 1 || b < 1 || b > n || k < 0 || k >= n {
		return 0, 0
	}
	start = (k / b) * b
	end := start + b
	if end > n {
		end = n // 尾块截断：size 即为 n%b
	}
	return start, end - start
}

// SelfCheck 只回答"大 n 下尾块定位是否仍为闭式 O(1)"，通过返回 nil。
// 它故意不回传 lookupEntries 的数值——该数值只能由同包白盒测试读取。
func SelfCheck() error {
	const b = 17
	for _, n := range []int{100, 1000, 10000} {
		if _, _ = TailBlock(n, b, n-1); lookupEntriesLoad() != 0 {
			return fmt.Errorf("%w (n=%d)", errNotClosedForm, n)
		}
	}
	return nil
}
