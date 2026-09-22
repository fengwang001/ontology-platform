// Package slotring 是底层定长环形数组：只负责按全局序号定位槽位、写入、读取。
// 它不持有 head/tail，也不判断覆盖；序号到槽位的映射是纯函数 (seq-1)%cap。
package slotring

import "fmt"

// Ring 是固定容量的环形数组，零值不可用，必须用 New 构造。
type Ring[T any] struct {
	slots []T
}

// New 创建容量为 cap 的环形数组；cap 必须为正数，由上层保证。
func New[T any](cap int) *Ring[T] {
	return &Ring[T]{slots: make([]T, cap)}
}

// Cap 返回槽位数量。
func (r *Ring[T]) Cap() int { return len(r.slots) }

// Index 返回全局序号 seq 对应的槽位下标。序号从 1 开始且永不复用。
func (r *Ring[T]) Index(seq int64) (int, error) {
	if seq <= 0 {
		return 0, fmt.Errorf("slotring: sequence must be positive, got %d", seq)
	}
	return int((seq - 1) % int64(len(r.slots))), nil
}

// Put 把值写入序号 seq 对应的槽位，并返回该槽位下标。
func (r *Ring[T]) Put(seq int64, v T) (int, error) {
	i, err := r.Index(seq)
	if err != nil {
		return 0, err
	}
	r.slots[i] = v
	return i, nil
}

// Get 读取序号 seq 对应的槽位当前内容。
func (r *Ring[T]) Get(seq int64) (T, int, error) {
	i, err := r.Index(seq)
	if err != nil {
		var zero T
		return zero, 0, err
	}
	return r.slots[i], i, nil
}
