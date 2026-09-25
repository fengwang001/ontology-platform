// Package check 提供朴素 FIFO 参照实现，用于对照测试。
package check

// Ref 用切片直接模拟 FIFO 队列。
type Ref struct {
	items []int
}

// Enqueue 入队。
func (r *Ref) Enqueue(v int) {
	r.items = append(r.items, v)
}

// Dequeue 出队；空队返回 (0, false)。
func (r *Ref) Dequeue() (int, bool) {
	if len(r.items) == 0 {
		return 0, false
	}
	v := r.items[0]
	r.items = r.items[1:]
	return v, true
}

// Peek 返回队首；空队返回 (0, false)。
func (r *Ref) Peek() (int, bool) {
	if len(r.items) == 0 {
		return 0, false
	}
	return r.items[0], true
}

// Len 返回队列长度。
func (r *Ref) Len() int {
	return len(r.items)
}
