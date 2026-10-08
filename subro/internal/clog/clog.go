// Package clog 提供分块只增日志。
//
// 普通切片追加是摊还 O(1)：容量翻倍时再分配会整体拷贝历史元素，
// 使单次追加的开销随历史长度周期性抖动。本包用定长块链表，
// 追加永不搬迁历史元素，最坏情况也是 O(1)，
// 用于支撑"每次操作后结清开销与历史无关"的可验证证明。
package clog

const chunkSize = 256

type node[T any] struct {
	data [chunkSize]T
	n    int
	next *node[T]
}

// Log 只增日志。零值即可用。
type Log[T any] struct {
	head *node[T]
	tail *node[T]
	n    int
}

// Append 追加一个元素，最坏情况 O(1)。
func (l *Log[T]) Append(v T) {
	if l.tail == nil || l.tail.n == chunkSize {
		nd := &node[T]{}
		if l.tail == nil {
			l.head = nd
		} else {
			l.tail.next = nd
		}
		l.tail = nd
	}
	l.tail.data[l.tail.n] = v
	l.tail.n++
	l.n++
}

// Len 返回元素个数。
func (l *Log[T]) Len() int {
	return l.n
}

// Slice 物化全部元素的副本。仅供查询与审计使用，
// 开销与日志长度成正比，不在每次操作的结清路径上调用。
func (l *Log[T]) Slice() []T {
	out := make([]T, 0, l.n)
	for nd := l.head; nd != nil; nd = nd.next {
		out = append(out, nd.data[:nd.n]...)
	}
	return out
}
