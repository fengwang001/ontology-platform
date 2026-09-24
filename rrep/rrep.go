// Package rrep 管理 n 个副本的读写、读修复回填与回填计数。依赖 rep。
package rrep

import (
	"container/heap"
	"sync"

	"ontology/rep"
)

// item 是 winner 索引（大根堆）的一条：某副本某一代的条目快照。
type item struct {
	ver int64
	val string
	r   int
	gen int64
}

type maxHeap []item

func (h maxHeap) Len() int { return len(h) }
func (h maxHeap) Less(i, j int) bool {
	if h[i].ver != h[j].ver {
		return h[i].ver > h[j].ver
	}
	return h[i].val > h[j].val
}
func (h maxHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *maxHeap) Push(x any)   { *h = append(*h, x.(item)) }
func (h *maxHeap) Pop() any {
	old := *h
	it := old[len(old)-1]
	*h = old[:len(old)-1]
	return it
}

type keyState struct {
	slots []rep.Slot
	gen   []int64 // 每副本写代数，用于堆里过期条目的惰性删除
	h     maxHeap
}

// Store 是 n 副本的内存存储。lastCmp 记录最近一次 Read 判定 winner 时比较过的条目数。
type Store struct {
	mu      sync.Mutex
	n       int
	keys    map[string]*keyState
	lastCmp int
}

// New 创建 n 副本存储（调用方保证 n>=1）。
func New(n int) *Store { return &Store{n: n, keys: map[string]*keyState{}} }

func (s *Store) keyState(key string) *keyState {
	ks := s.keys[key]
	if ks == nil {
		ks = &keyState{slots: make([]rep.Slot, s.n), gen: make([]int64, s.n)}
		s.keys[key] = ks
	}
	return ks
}

func (ks *keyState) write(r int, val string, ver int64) {
	ks.slots[r] = rep.Slot{Value: val, Ver: ver, Ok: true}
	ks.gen[r]++
	heap.Push(&ks.h, item{ver: ver, val: val, r: r, gen: ks.gen[r]})
}

// Put 把 (val,ver) 写到 reps 的每个副本，其余不动。调用方保证参数合法。
func (s *Store) Put(key, val string, ver int64, reps []int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ks := s.keyState(key)
	for _, r := range reps {
		ks.write(r, val, ver)
	}
}

// Read 判定 winner 并回填空/落后/冲突副本，返回 winner 与被回填副本数。
// 全部副本为空（或 key 不存在）时返回的 Slot.Ok=false。
func (s *Store) Read(key string) (rep.Slot, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastCmp = 0
	ks := s.keys[key]
	if ks == nil {
		return rep.Slot{}, 0
	}
	for len(ks.h) > 0 {
		s.lastCmp++
		top := ks.h[0]
		if top.gen != ks.gen[top.r] { // 过期索引条目，惰性弹出
			heap.Pop(&ks.h)
			continue
		}
		w := rep.Slot{Value: top.val, Ver: top.ver, Ok: true}
		repaired := 0
		for i := range ks.slots {
			if rep.Classify(ks.slots[i], w) != rep.Current {
				ks.write(i, w.Value, w.Ver)
				repaired++
			}
		}
		return w, repaired
	}
	return rep.Slot{}, 0
}

// Snapshot 返回 key 当前各副本条目的副本（未知 key 全空）。
func (s *Store) Snapshot(key string) []rep.Slot {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]rep.Slot, s.n)
	if ks := s.keys[key]; ks != nil {
		copy(out, ks.slots)
	}
	return out
}
