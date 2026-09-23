// Package aging 实现带老化防饥饿的优先级任务队列（依赖 clock、heapq）。
package aging

import (
	"errors"
	"sync"

	"ontology/clock"
	"ontology/heapq"
)

var (
	ErrNotFound  = errors.New("aging: id not found")            // 不存在或已出队
	ErrDuplicate = errors.New("aging: duplicate id")            // id 已在队列中
	ErrFull      = errors.New("aging: queue full")              // 达到容量上限
	ErrRange     = errors.New("aging: value out of safe range") // K<=0 或越界
)

// safeBound：|t|、K|base| < 2^62 时 key=t-K*base 不溢出 int64。
const safeBound = int64(1) << 62

// InRange 暴露范围检查，供朴素参照实现复用同一判定。
func InRange(t, base, k int64) error { return inRange(t, base, k) }

type entry struct {
	id      int
	base, t int64
}

// Queue 是并发安全的老化优先级队列，状态仅存于进程内存。
type Queue struct {
	mu    sync.Mutex
	clk   *clock.Clock
	k     int64
	max   int
	heap  *heapq.Heap[entry]
	items map[int]*heapq.Item[entry]
}

// New 创建队列：K 为老化除数，maxLen 为容量上限（<=0 表示不限）。
func New(clk *clock.Clock, k, maxLen int) (*Queue, error) {
	if k <= 0 {
		return nil, ErrRange
	}
	q := &Queue{clk: clk, k: int64(k), max: maxLen, items: map[int]*heapq.Item[entry]{}}
	q.heap = heapq.New[entry](q.less)
	return q, nil
}

// less 按比较元组 (key=t-K*base, t, id) 升序；key 与 now 无关，推进时钟零重排。
func (q *Queue) less(a, b entry) bool {
	ka, kb := a.t-q.k*a.base, b.t-q.k*b.base
	if ka != kb {
		return ka < kb
	}
	if a.t != b.t {
		return a.t < b.t
	}
	return a.id < b.id
}

// Push 在当前刻以基础优先级 base 入队 id；各类错误均零副作用。
func (q *Queue) Push(id, base int) error {
	now, b := q.clk.Now(), int64(base)
	if err := inRange(now, b, q.k); err != nil {
		return err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, ok := q.items[id]; ok {
		return ErrDuplicate
	}
	if q.max > 0 && len(q.items) >= q.max {
		return ErrFull
	}
	q.items[id] = q.heap.Push(entry{id: id, base: b, t: now})
	return nil
}

// inRange 检查 |t|、K|base| < safeBound；只用除法，杜绝乘法溢出。
func inRange(t, b, k int64) error {
	if t <= -safeBound || t >= safeBound {
		return ErrRange
	}
	c := b
	if c < 0 {
		c = -c
		if c < 0 { // -b 溢出（b=MinInt64），必越界
			return ErrRange
		}
	}
	if c != 0 && k > (safeBound-1)/c {
		return ErrRange
	}
	return nil
}

// Pop 移出并返回当前有效优先级最大的任务；空队列 ok=false。
func (q *Queue) Pop() (int, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	e, ok := q.heap.Pop()
	if !ok {
		return 0, false
	}
	delete(q.items, e.id)
	return e.id, true
}

// Remove 移除任务；id 不存在或已出队返回 ErrNotFound 且零副作用。
func (q *Queue) Remove(id int) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	it, ok := q.items[id]
	if !ok {
		return ErrNotFound
	}
	q.heap.Remove(it)
	delete(q.items, id)
	return nil
}

// Len 返回在队任务数。
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

// CmpCount / ResetCmpCount 暴露 heapq 的非导出比较计数器。
func (q *Queue) CmpCount() uint64 { return q.heap.CmpCount() }
func (q *Queue) ResetCmpCount()   { q.heap.ResetCmpCount() }
