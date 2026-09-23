// Package keyq 维护“每个键一个 FIFO 任务队列”。
// 它不依赖本工程其他任何包，自带一把锁保护内部 map。
package keyq

import "sync"

// Fn 是用户提交的任务单元。
type Fn func()

// Q 是全部键的队列集合。零值不可用，必须用 New 构造。
type Q struct {
	mu sync.Mutex
	qs map[string][]Fn
}

// New 创建空的队列集合。
func New() *Q { return &Q{qs: make(map[string][]Fn)} }

// Push 把 fn 追加到 key 的 FIFO 队尾。
func (q *Q) Push(key string, fn Fn) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.qs[key] = append(q.qs[key], fn)
}

// Pop 取走 key 的队首任务；队列随后为空时删除该键，
// 因此空闲键不占用任何存储。队列空返回 nil。
func (q *Q) Pop(key string) Fn {
	q.mu.Lock()
	defer q.mu.Unlock()
	s := q.qs[key]
	if len(s) == 0 {
		delete(q.qs, key)
		return nil
	}
	fn := s[0]
	if len(s) == 1 {
		delete(q.qs, key)
	} else {
		q.qs[key] = s[1:]
	}
	return fn
}

// Len 返回 key 当前排队任务数。
func (q *Q) Len(key string) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.qs[key])
}

// Keys 返回当前非空队列的键数。
func (q *Q) Keys() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.qs)
}
