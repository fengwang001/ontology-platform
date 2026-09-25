// Package ttl 按 Key 维护状态、逻辑时钟单调校验与 Set/Get/Sweep/View。
// 依赖 entry，不依赖 api。
package ttl

import (
	"container/heap"
	"errors"
	"sync"

	"ontology/entry"
)

// 可判定的哨兵错误，互不相同。
var (
	ErrBackwardClock = errors.New("ttl: backward clock")
	ErrEmptyKey      = errors.New("ttl: empty key")
)

// Store 是带 TTL 上限的进程内状态存储。ttl 必须 > 0（由调用方保证）。
type Store struct {
	mu      sync.Mutex
	ttl     int64
	maxNow  int64 // 已见过的时钟上界
	hasNow  bool
	items   map[string]entry.Entry
	h       tsHeap // 按 Ts 的最小堆，惰性删除（陈旧项在 Sweep 时跳过）
	checked int    // 非导出：最近一次 Sweep 检查（peek/pop）过的 key 个数
}

// New 构造存储；调用方必须保证 ttl > 0。
func New(ttl int64) *Store {
	return &Store{ttl: ttl, items: make(map[string]entry.Entry)}
}

// checkClock 在任何状态修改之前调用；回退则拒绝且不改变时钟上界。
func (s *Store) checkClock(now int64) error {
	if s.hasNow && now < s.maxNow {
		return ErrBackwardClock
	}
	return nil
}

func (s *Store) advance(now int64) { s.maxNow, s.hasNow = now, true }

// Set 写入/更新 key 并置 Ts = now。失败不留痕：先校验，后修改。
func (s *Store) Set(key, value string, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if key == "" {
		return ErrEmptyKey
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	s.advance(now)
	s.items[key] = entry.Entry{Value: value, Ts: now}
	heap.Push(&s.h, heapItem{key: key, ts: now})
	return nil
}

// Get 惰性删除：过期（含等于）立即删除并返回 ok=false。不刷新 Ts。
// 时钟回退或空 key 时被拒绝：返回 ok=false，不改变任何状态。
func (s *Store) Get(key string, now int64) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if key == "" || s.checkClock(now) != nil {
		return "", false
	}
	s.advance(now)
	e, ok := s.items[key]
	if !ok {
		return "", false
	}
	if entry.Expired(now, e.Ts, s.ttl) {
		delete(s.items, key)
		return "", false
	}
	return e.Value, true
}

// Sweep 主动清理所有过期 key，返回删除个数。
func (s *Store) Sweep(now int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return 0, err
	}
	s.advance(now)
	return s.sweepLocked(now), nil
}

// sweepLocked 只弹出堆顶过期项，遇到队首未过期即停，不整表扫描。
func (s *Store) sweepLocked(now int64) int {
	s.checked = 0
	deleted := 0
	for s.h.Len() > 0 {
		top := s.h[0]
		s.checked++
		e, ok := s.items[top.key]
		if !ok || e.Ts != top.ts { // 陈旧堆项：key 已删或已被更新的写入覆盖
			heap.Pop(&s.h)
			continue
		}
		if !entry.Expired(now, e.Ts, s.ttl) {
			break
		}
		delete(s.items, top.key)
		heap.Pop(&s.h)
		deleted++
	}
	return deleted
}

// View 等价于先 Sweep(now) 再全量列出未过期 key。
func (s *Store) View(now int64) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return nil, err
	}
	s.advance(now)
	s.sweepLocked(now)
	out := make(map[string]string, len(s.items))
	for k, e := range s.items {
		out[k] = e.Value
	}
	return out, nil
}

// heapItem 是堆项；key 被重复 Set 会留下陈旧项，由 sweepLocked 跳过。
type heapItem struct {
	key string
	ts  int64
}

type tsHeap []heapItem

func (h tsHeap) Len() int           { return len(h) }
func (h tsHeap) Less(i, j int) bool { return h[i].ts < h[j].ts }
func (h tsHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *tsHeap) Push(x any)        { *h = append(*h, x.(heapItem)) }
func (h *tsHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}
