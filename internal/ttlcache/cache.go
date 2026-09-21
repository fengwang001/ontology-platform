// Package ttlcache 实现一个带 TTL 的 LRU 缓存。
// 时间完全由外部注入（now 函数，单位毫秒），实现内部不调用 time.Now。
package ttlcache

import (
	"container/list"
	"sync"
)

// entry 是缓存中的单个条目。expireAt 在首次写入时确定，
// 之后对同键的 Put 不会刷新它。
type entry struct {
	key      string
	val      string
	writeAt  int64
	expireAt int64
}

// expired 判定「到点即过期」：now >= expireAt 即视为过期。
func (e *entry) expired(now int64) bool {
	return now >= e.expireAt
}

// Cache 是带 TTL 的 LRU 缓存，可安全并发使用。
type Cache struct {
	mu    sync.Mutex
	cap   int
	now   func() int64
	ll    *list.List // 前端为最近使用，后端为最久未使用；元素为 *entry
	items map[string]*list.Element
}

// New 创建一个容量为 capacity 的缓存，now 返回当前逻辑时刻（毫秒）。
func New(capacity int, now func() int64) (*Cache, error) {
	if capacity <= 0 {
		return nil, ErrInvalidCapacity
	}
	if now == nil {
		return nil, ErrNilClock
	}
	return &Cache{
		cap:   capacity,
		now:   now,
		ll:    list.New(),
		items: make(map[string]*list.Element),
	}, nil
}

// Put 写入或更新一个键。已存在的键只更新值并提升为最近使用，
// 不刷新 TTL。ttlMillis <= 0 时返回 ErrInvalidTTL。
func (c *Cache) Put(key, val string, ttlMillis int64) error {
	if ttlMillis <= 0 {
		return ErrInvalidTTL
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if el, ok := c.items[key]; ok {
		el.Value.(*entry).val = val
		c.ll.MoveToFront(el)
		return nil
	}

	now := c.now()
	if c.ll.Len() >= c.cap {
		c.evictLocked(now)
	}
	e := &entry{key: key, val: val, writeAt: now, expireAt: now + ttlMillis}
	c.items[key] = c.ll.PushFront(e)
	return nil
}

// evictLocked 在容量已满时驱逐一个条目：优先驱逐已过期项中
// 写入时刻最早者；没有过期项时驱逐最久未使用（链表后端）的项。
func (c *Cache) evictLocked(now int64) {
	var victim *list.Element
	for el := c.ll.Front(); el != nil; el = el.Next() {
		e := el.Value.(*entry)
		if !e.expired(now) {
			continue
		}
		if victim == nil || e.writeAt < victim.Value.(*entry).writeAt {
			victim = el
		}
	}
	if victim == nil {
		victim = c.ll.Back()
	}
	if victim != nil {
		c.removeLocked(victim)
	}
}

func (c *Cache) removeLocked(el *list.Element) {
	c.ll.Remove(el)
	delete(c.items, el.Value.(*entry).key)
}

// Get 读取一个键。命中未过期项时提升为最近使用；
// 命中已过期项时返回 miss 并立即删除该项。
func (c *Cache) Get(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	el, ok := c.items[key]
	if !ok {
		return "", false
	}
	e := el.Value.(*entry)
	if e.expired(c.now()) {
		c.removeLocked(el)
		return "", false
	}
	c.ll.MoveToFront(el)
	return e.val, true
}

// Delete 删除一个键，返回是否真的删掉了东西。
// 删除已过期但尚未清理的项也算删掉，返回 true。
func (c *Cache) Delete(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	el, ok := c.items[key]
	if !ok {
		return false
	}
	c.removeLocked(el)
	return true
}

// Len 返回当前条目数，尚未清理的过期项也计入。
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}
