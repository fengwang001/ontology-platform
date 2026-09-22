// Package ttlcache 实现一个时间完全由外部注入的带 TTL 的 LRU 缓存。
package ttlcache

import (
	"container/heap"
	"container/list"
)

type entry struct {
	key      string
	val      string
	writeAt  int64
	expireAt int64
	tick     int64 // 最近一次被触碰（写入/更新/命中）的逻辑序号
	candIdx  int   // 在 cand 堆中的下标
	expIdx   int   // 在 exp 堆中的下标
}

// Cache 是带 TTL 的 LRU 缓存，非并发安全。
type Cache struct {
	capacity int
	now      func() int64
	items    map[string]*list.Element
	lru      *list.List // 队首为最近使用
	cand     candHeap   // 驱逐候选堆：(writeAt 升序, tick 降序)
	exp      expHeap    // 过期堆：expireAt 升序
	tickSeq  int64      // 触碰序号发生器，越大越最近使用
	// lastExamined 记录最近一次驱逐考察了多少个候选项，仅供测试与演示观测。
	lastExamined int
}

// New 创建一个容量为 capacity 的缓存，now 返回当前逻辑时刻（毫秒）。
func New(capacity int, now func() int64) (*Cache, error) {
	if capacity <= 0 {
		return nil, ErrInvalidCapacity
	}
	return &Cache{
		capacity: capacity,
		now:      now,
		items:    make(map[string]*list.Element),
		lru:      list.New(),
	}, nil
}

// Put 写入或更新键值对，ttlMillis 必须为正数。
// 更新已存在的键时不刷新 TTL，过期时刻仍以最初写入为准。
func (c *Cache) Put(key, val string, ttlMillis int64) error {
	if ttlMillis <= 0 {
		return ErrInvalidTTL
	}
	if el, ok := c.items[key]; ok {
		e := el.Value.(*entry)
		e.val = val
		c.lru.MoveToFront(el)
		c.touch(e)
		return nil
	}
	if len(c.items) >= c.capacity {
		c.evict()
	}
	now := c.now()
	e := &entry{
		key:      key,
		val:      val,
		writeAt:  now,
		expireAt: now + ttlMillis,
	}
	el := c.lru.PushFront(e)
	c.items[key] = el
	heap.Push(&c.cand, e)
	heap.Push(&c.exp, e)
	c.touch(e)
	return nil
}

// Get 读取键对应的值；命中过期项时返回 miss 并删除该项。
func (c *Cache) Get(key string) (string, bool) {
	el, ok := c.items[key]
	if !ok {
		return "", false
	}
	e := el.Value.(*entry)
	if c.expired(e) {
		c.remove(el)
		return "", false
	}
	c.lru.MoveToFront(el)
	c.touch(e)
	return e.val, true
}

// Delete 删除键，返回是否真的删掉了东西（含已过期未清理的项）。
func (c *Cache) Delete(key string) bool {
	el, ok := c.items[key]
	if !ok {
		return false
	}
	c.remove(el)
	return true
}

// Len 返回当前项数，包含尚未清理的过期项。
func (c *Cache) Len() int {
	return len(c.items)
}

// expired 判定到点即过期：now >= expireAt 视为已过期。
func (c *Cache) expired(e *entry) bool {
	return c.now() >= e.expireAt
}

func (c *Cache) remove(el *list.Element) {
	e := el.Value.(*entry)
	delete(c.items, e.key)
	c.lru.Remove(el)
	heap.Remove(&c.cand, e.candIdx)
	heap.Remove(&c.exp, e.expIdx)
}

// touch 记录一次使用：刷新项的触碰序号并维护候选堆。
func (c *Cache) touch(e *entry) {
	c.tickSeq++
	e.tick = c.tickSeq
	heap.Fix(&c.cand, e.candIdx)
}

// evict 驱逐一项：优先驱逐已过期项中写入时刻最早者，
// 没有过期项时驱逐最久未使用的项。
// 并列规则：多个已过期项写入时刻完全相同时，驱逐其中最近使用者
// （cand 堆按 writeAt 升序、tick 降序排列，堆顶即该规则下的首选，
// 与旧实现“从队首扫描、仅严格更小时替换”选出的项一致）。
//
// 候选选取不再是线性扫描：先用 exp 堆顶 O(1) 判定是否存在过期项，
// 再从 cand 堆顶逐个弹出，第一个已过期者即被驱逐者，
// 途中弹出的未过期项在结束后压回。考察的候选数记入 lastExamined。
func (c *Cache) evict() {
	now := c.now()
	examined := 0
	var victim *entry
	if len(c.exp) > 0 {
		examined++ // exp 堆顶是最早过期者：它未过期则没有任何过期项
		if c.exp[0].expireAt <= now {
			var stash []*entry
			for len(c.cand) > 0 {
				e := c.cand[0]
				examined++
				if e.expireAt <= now {
					victim = e
					break
				}
				stash = append(stash, heap.Pop(&c.cand).(*entry))
			}
			for _, e := range stash {
				heap.Push(&c.cand, e)
			}
		}
	}
	c.lastExamined = examined
	if victim != nil {
		c.remove(c.items[victim.key])
		return
	}
	c.remove(c.lru.Back())
}
