// Package ontology 提供代价感知的构建制品缓存。
//
// 缓存按 Greedy-Dual-Size-Frequency（GDSF）策略在字节容量限制下驱逐制品，
// 全程使用 math/big.Rat 精确有理数，不使用任何浮点运算。
package ontology

import (
	"container/heap"
	"errors"
	"math/big"
	"sync"
)

// 可区分的拒绝原因。调用方可用 errors.Is 判定。
var (
	ErrInvalidCapacity = errors.New("capacity must be greater than 0")
	ErrEmptyKey        = errors.New("key must not be empty")
	ErrInvalidSize     = errors.New("size must be a positive integer")
	ErrInvalidCost     = errors.New("cost must be an integer >= 1")
	ErrObjectTooLarge  = errors.New("size exceeds capacity")
)

// EntryView 是 Peek 返回的只读快照。
type EntryView struct {
	Freq int64
	H    *big.Rat
	Last int64
}

// Cache 是代价感知构建制品缓存。零值不可用，请使用 NewCache 构造。
type Cache struct {
	mu    sync.Mutex
	cap   int64
	used  int64
	tick  int64
	L     *big.Rat
	items map[string]*entry
	pq    gdsfHeap

	// cmpCount 为非导出的 H 比较次数计数器，用于复杂度证明。
	cmpCount int64
}

// entry 是缓存内部条目。H 保存“插入或命中那一刻”按当时 L 计算的优先级。
type entry struct {
	key  string
	size int64
	cost int64
	freq int64
	h    *big.Rat
	last int64
	idx  int
}

// gdsfHeap 以 (H, last) 为序：H 小者优先，H 相等时 last 小者优先。
type gdsfHeap struct {
	entries []*entry
	cmp     *int64
}

func (h gdsfHeap) Len() int { return len(h.entries) }

func (h gdsfHeap) Less(i, j int) bool {
	// 每一次 H 比较都计入计数器；H 相等时比较 last 不再额外计 H 比较。
	*h.cmp++
	cmp := h.entries[i].h.Cmp(h.entries[j].h)
	if cmp == 0 {
		return h.entries[i].last < h.entries[j].last
	}
	return cmp < 0
}

func (h gdsfHeap) Swap(i, j int) {
	h.entries[i], h.entries[j] = h.entries[j], h.entries[i]
	h.entries[i].idx = i
	h.entries[j].idx = j
}

func (h *gdsfHeap) Push(x any) {
	e := x.(*entry)
	e.idx = len(h.entries)
	h.entries = append(h.entries, e)
}

func (h *gdsfHeap) Pop() any {
	old := h.entries
	n := len(old)
	e := old[n-1]
	old[n-1] = nil
	e.idx = -1
	h.entries = old[:n-1]
	return e
}

// score 精确计算 H = L + freq*cost/size。所有运算均为精确有理数，不使用浮点。
func score(L *big.Rat, freq, cost, size int64) *big.Rat {
	return new(big.Rat).Add(L, new(big.Rat).SetFrac64(freq*cost, size))
}

// NewCache 以字节容量 capBytes 构造缓存。capBytes <= 0 时返回错误。
func NewCache(capBytes int64) (*Cache, error) {
	if capBytes <= 0 {
		return nil, ErrInvalidCapacity
	}
	c := &Cache{
		cap:   capBytes,
		L:     big.NewRat(0, 1),
		items: make(map[string]*entry),
	}
	c.pq.cmp = &c.cmpCount
	return c, nil
}

// Put 插入或覆盖一个制品，返回本次被驱逐的键（按驱逐先后）。
func (c *Cache) Put(key string, size, cost int64) (evicted []string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// 拒绝次序固定：键为空 → size 不大于 0 → cost 小于 1 → size 大于 Cap。
	// 校验全部发生在任何状态变更之前，因此被拒绝的操作不改变条目、L 与 tick。
	if key == "" {
		return nil, ErrEmptyKey
	}
	if size <= 0 {
		return nil, ErrInvalidSize
	}
	if cost < 1 {
		return nil, ErrInvalidCost
	}
	if size > c.cap {
		return nil, ErrObjectTooLarge
	}

	c.cmpCount = 0

	// 键已存在：先移除旧条目（释放字节，不改变 L），再走插入流程。
	if old, ok := c.items[key]; ok {
		heap.Remove(&c.pq, old.idx)
		delete(c.items, old.key)
		c.used -= old.size
	}

	// 已用字节加 size 大于 Cap 时反复驱逐；恰等于 Cap 时不驱逐。
	for c.used+size > c.cap {
		victim := heap.Pop(&c.pq).(*entry)
		// 每驱逐一个，L 等于被驱逐者的 H（逐个单调推进）。
		c.L.Set(victim.h)
		c.used -= victim.size
		delete(c.items, victim.key)
		evicted = append(evicted, victim.key)
	}

	// 插入新条目。不做准入过滤：其 H 允许低于现有条目。
	e := &entry{
		key:  key,
		size: size,
		cost: cost,
		freq: 1,
		h:    score(c.L, 1, cost, size),
		last: c.tick,
	}
	heap.Push(&c.pq, e)
	c.items[key] = e
	c.used += size

	// 成功的 Put 插入使 tick 加 1。
	c.tick++
	return evicted, nil
}

// Get 查询制品。命中提升优先级并返回 true，未命中返回 false 且不改状态。
func (c *Cache) Get(key string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if key == "" {
		return false, ErrEmptyKey
	}
	e, ok := c.items[key]
	if !ok {
		// 未命中不改变任何状态，也不触碰比较计数器。
		return false, nil
	}

	c.cmpCount = 0
	e.freq++
	// 命中后用“当前 L”重算 H，而非沿用旧 H。
	e.h = score(c.L, e.freq, e.cost, e.size)
	e.last = c.tick
	heap.Fix(&c.pq, e.idx)
	c.tick++
	return true, nil
}

// Peek 返回条目快照但不改变任何状态。
func (c *Cache) Peek(key string) (EntryView, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if key == "" {
		return EntryView{}, false, ErrEmptyKey
	}
	e, ok := c.items[key]
	if !ok {
		return EntryView{}, false, nil
	}
	// 返回 H 的副本，避免调用方经指针改动内部状态。
	return EntryView{Freq: e.freq, H: new(big.Rat).Set(e.h), Last: e.last}, true, nil
}

// Used 返回当前已用字节。
func (c *Cache) Used() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.used
}

// inflation 仅供测试读取全局通胀值 L。
func (c *Cache) inflation() *big.Rat {
	c.mu.Lock()
	defer c.mu.Unlock()
	return new(big.Rat).Set(c.L)
}

// lastComparisons 返回上一次成功的 Put/Get 期间发生的 H 比较次数。
func (c *Cache) lastComparisons() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cmpCount
}
