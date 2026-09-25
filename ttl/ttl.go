// Package ttl 按 Key 维护带 TTL 上限的进程内状态，依赖 entry。
// 过期判定 now−Ts ≥ ttl；惰性删除（Get）与主动清理（Sweep）并用；
// 逻辑时钟只进不退；按 Ts 的最小堆保证 Sweep 只查到队首未过期即停。
package ttl

import (
	"container/heap"
	"errors"
	"sync"

	"ontology/entry"
)

var (
	// ErrBackwardClock 表示本次 now 小于已见过的最大 now。
	ErrBackwardClock = errors.New("ttl: backward clock")
	// ErrEmptyKey 表示 key 为空串。
	ErrEmptyKey = errors.New("ttl: empty key")
)

// item 是堆元素；同一 key 多次写入会留下过期（stale）元素，靠 ts 与 map 对账跳过。
type item struct {
	key string
	ts  int64
}

type minHeap []item

func (h minHeap) Len() int           { return len(h) }
func (h minHeap) Less(i, j int) bool { return h[i].ts < h[j].ts }
func (h minHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *minHeap) Push(x any)        { *h = append(*h, x.(item)) }
func (h *minHeap) Pop() any {
	old := *h
	it := old[len(old)-1]
	*h = old[:len(old)-1]
	return it
}

// Store 是 Key→Value 的进程内存储，TTL 上限由 ttl 给定。并发安全。
type Store struct {
	mu      sync.Mutex
	ttl     int64
	maxNow  int64 // 已见过的最大逻辑时钟
	hasNow  bool
	items   map[string]entry.Entry
	h       minHeap
	checked int // 非导出：最近一次 Sweep 检查（peek/pop）过的 key 个数
}

// New 构造存储；ttl 的合法性由上层（api）校验。
func New(ttl int64) *Store {
	return &Store{ttl: ttl, items: make(map[string]entry.Entry)}
}

// checkClock 强制时钟只进不退；拒绝时不改变任何状态。
func (s *Store) checkClock(now int64) error {
	if s.hasNow && now < s.maxNow {
		return ErrBackwardClock
	}
	s.maxNow, s.hasNow = now, true
	return nil
}

// Set 写入/更新 key 的值并置 Ts=now。
func (s *Store) Set(key, value string, now int64) error {
	if key == "" {
		return ErrEmptyKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	s.items[key] = entry.Entry{Value: value, Ts: now}
	heap.Push(&s.h, item{key: key, ts: now})
	return nil
}

// Get 读取 key；过期（now−Ts ≥ ttl）立即删除并返回 ok=false；不刷新 Ts。
func (s *Store) Get(key string, now int64) (string, bool, error) {
	if key == "" {
		return "", false, ErrEmptyKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return "", false, err
	}
	e, ok := s.items[key]
	if !ok {
		return "", false, nil
	}
	if entry.Expired(e.Ts, now, s.ttl) {
		delete(s.items, key) // 堆中元素成为 stale，由 Sweep 跳过
		return "", false, nil
	}
	return e.Value, true, nil
}

// sweepLocked 弹出堆顶所有过期（含 stale）元素，返回实际删除的 key 数。
// 每检查一个堆顶元素 checked 加一；遇首个未过期即停，不整表扫描。
func (s *Store) sweepLocked(now int64) int {
	s.checked = 0
	n := 0
	for len(s.h) > 0 {
		top := s.h[0]
		s.checked++
		cur, ok := s.items[top.key]
		if ok && cur.Ts == top.ts && !entry.Expired(top.ts, now, s.ttl) {
			break // 队首未过期，其后更不会过期
		}
		heap.Pop(&s.h)
		if ok && cur.Ts == top.ts {
			delete(s.items, top.key)
			n++
		}
	}
	return n
}

// Sweep 主动清理所有过期 key，返回删除个数。
func (s *Store) Sweep(now int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return 0, err
	}
	return s.sweepLocked(now), nil
}

// View 返回所有未过期 key 的快照（等价于先 Sweep 再全量列出）。
func (s *Store) View(now int64) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return nil, err
	}
	s.sweepLocked(now)
	out := make(map[string]string, len(s.items))
	for k, e := range s.items {
		out[k] = e.Value
	}
	return out, nil
}
