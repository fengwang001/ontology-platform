// Package gc 实现 CRDT 的 G 计数器（grow-only counter）：
// 节点 id -> 计数的稀疏映射，计数只增不减。
package gc

import (
	"errors"
	"sync/atomic"
)

// ErrNegativeEntry 表示计数器含负条目（count < 0）。
var ErrNegativeEntry = errors.New("gc: negative entry")

// Counter 是节点 id -> 计数的稀疏映射。缺失条目视为 0。
type Counter map[int]int

// lastMergeReads 记录最近一次 Merge 读取过的条目个数。
// 非导出，不出现在任何公开接口；仅包内测试可直接观测。
var lastMergeReads atomic.Int64

// Merge 返回 a 与 b 逐条目取 max 的新计数器；a、b 均不被修改。
// 任一计数器含负条目时整体失败，不产生任何结果。
func Merge(a, b Counter) (Counter, error) {
	reads := 0
	out := make(Counter, len(a)+len(b))
	for node, cnt := range a {
		if cnt < 0 {
			return nil, ErrNegativeEntry
		}
		reads++
		out[node] = cnt
	}
	for node, cnt := range b {
		if cnt < 0 {
			return nil, ErrNegativeEntry
		}
		reads++
		if cnt > out[node] {
			out[node] = cnt
		}
	}
	lastMergeReads.Store(int64(reads))
	return out, nil
}

// Value 返回计数器所有条目之和。
func Value(c Counter) int {
	sum := 0
	for _, cnt := range c {
		sum += cnt
	}
	return sum
}
