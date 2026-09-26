// Package vv 提供版本向量类型与两个纯函数：Merge（逐 key 取 max 的 join）
// 与 Compare（三歧 + 并发的因果比较）。本包不依赖工程内其他包。
package vv

import "sync/atomic"

// Vector 是 actor id -> 计数器的稀疏映射；缺失的 key 等价于 0。
type Vector = map[int]int

// Order 是 Compare 的四态结果。
type Order int

const (
	Equal      Order = iota // 逐 key 全相等
	Less                    // A <= B 且存在严格小
	Greater                 // A >= B 且存在严格大
	Concurrent              // 既非 A<=B 也非 A>=B
)

// String 返回可读的比较结果名。
func (o Order) String() string {
	switch o {
	case Equal:
		return "Equal"
	case Less:
		return "Less"
	case Greater:
		return "Greater"
	default:
		return "Concurrent"
	}
}

// mergeCounters 维护最近一次 Merge 读取过的向量条目数。
// 字段非导出，包外无法读取（测试只能在 vv 包内访问）。
type mergeStats struct {
	reads atomic.Int64
}

var stats mergeStats

// Merge 返回 a、b 在所有 key 并集上逐 key 取 max 的结果。纯函数，不改输入。
// 只读取两个稀疏映射中实际存在的条目，读取数与 actor id 空间大小无关。
// 输出是规范稀疏形式：值为 0 的条目不写入（缺失本就等价于 0）。
func Merge(a, b Vector) Vector {
	out := make(Vector, len(a)+len(b))
	var reads int64
	for k, va := range a {
		reads++
		if va > 0 {
			out[k] = va
		}
	}
	for k, vb := range b {
		reads++
		if vb > out[k] {
			out[k] = vb
		}
	}
	stats.reads.Store(reads)
	return out
}

// Compare 按缺失视为 0 的逐 key 定义返回因果关系。
func Compare(a, b Vector) Order {
	less, greater := false, false
	for k, va := range a {
		if vb := b[k]; va < vb {
			less = true
		} else if va > vb {
			greater = true
		}
	}
	for k, vb := range b {
		if _, ok := a[k]; !ok && vb > 0 {
			less = true // a[k] 缺失视为 0
		}
	}
	switch {
	case less && greater:
		return Concurrent
	case less:
		return Less
	case greater:
		return Greater
	default:
		return Equal
	}
}
