// Package keyq 维护每个键一个 FIFO 任务队列。它不依赖其他包，
// 自身不加锁；并发安全由调用方（exec）用一把互斥锁统一保证。
package keyq

// Queues 是「键 -> 该键的任务队列」的集合。
type Queues[K comparable, V any] struct {
	q map[K][]V
}

func New[K comparable, V any]() *Queues[K, V] {
	return &Queues[K, V]{q: make(map[K][]V)}
}

// Push 把 v 追加到键的队尾，返回该键在 push 之前是否为空。
func (m *Queues[K, V]) Push(key K, v V) (wasEmpty bool) {
	q := m.q[key]
	wasEmpty = len(q) == 0
	m.q[key] = append(q, v)
	return wasEmpty
}

// Pop 取出并删除键的队首任务；空队列时 ok 为 false。
func (m *Queues[K, V]) Pop(key K) (v V, ok bool) {
	q := m.q[key]
	if len(q) == 0 {
		return v, false
	}
	v = q[0]
	q = q[1:]
	if len(q) == 0 {
		delete(m.q, key)
	} else {
		m.q[key] = q
	}
	return v, true
}

// Len 返回指定键当前排队的任务数。
func (m *Queues[K, V]) Len(key K) int { return len(m.q[key]) }

// Keys 返回当前有排队任务的键数。
func (m *Queues[K, V]) Keys() int { return len(m.q) }
