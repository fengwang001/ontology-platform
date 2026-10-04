// Package reuse 实现解除抑制（reuse）时刻的调度。
//
// 调度器是小顶堆与键索引的组合，按 (reuseAt, 邻居, 前缀) 次序
// 弹出全部 reuseAt≤now 的条目，保证解除顺序是时间的纯函数。
package reuse

import "container/heap"

// Key 是路由键：邻居与前缀编号。
type Key struct {
	Peer   int64
	Prefix int64
}

// Item 是一条到期的解除抑制条目。
type Item struct {
	Key Key
	At  int64
}

type entry struct {
	key   Key
	at    int64
	index int
}

type entryHeap []*entry

func (h entryHeap) Len() int { return len(h) }

func (h entryHeap) Less(i, j int) bool {
	a, b := h[i], h[j]
	if a.at != b.at {
		return a.at < b.at
	}
	if a.key.Peer != b.key.Peer {
		return a.key.Peer < b.key.Peer
	}
	return a.key.Prefix < b.key.Prefix
}

func (h entryHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *entryHeap) Push(x any) {
	e := x.(*entry)
	e.index = len(*h)
	*h = append(*h, e)
}

func (h *entryHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return e
}

// Scheduler 调度各路由的解除抑制时刻。非并发安全，由调用方串行化。
type Scheduler struct {
	h   entryHeap
	pos map[Key]*entry
}

// NewScheduler 返回空调度器。
func NewScheduler() *Scheduler {
	return &Scheduler{pos: make(map[Key]*entry)}
}

// Len 返回调度中的条目数。
func (s *Scheduler) Len() int { return len(s.h) }

// Upsert 插入或更新键 k 的解除时刻。
func (s *Scheduler) Upsert(k Key, at int64) {
	if e, ok := s.pos[k]; ok {
		e.at = at
		heap.Fix(&s.h, e.index)
		return
	}
	e := &entry{key: k, at: at}
	s.pos[k] = e
	heap.Push(&s.h, e)
}

// PopDue 移除并按 (At, Peer, Prefix) 次序返回全部 At≤now 的条目。
func (s *Scheduler) PopDue(now int64) []Item {
	var out []Item
	for len(s.h) > 0 && s.h[0].at <= now {
		e := heap.Pop(&s.h).(*entry)
		delete(s.pos, e.key)
		out = append(out, Item{Key: e.key, At: e.at})
	}
	return out
}
