// Package ttlcache 实现一个带 TTL 的 LRU 缓存。
// 时间完全由外部注入（now 函数，单位毫秒），实现内部不调用 time.Now。
package ttlcache

import (
	"container/list"
	"sync"
)

// entry 是缓存中的单个条目。
type entry struct {
	key       string
	val       string
	createdAt int64 // 首次写入时刻（毫秒）
	expireAt  int64 // 过期时刻，now >= expireAt 即视为过期
}

// Cache 是带 TTL 的 LRU 缓存，可安全并发使用。
type Cache struct {
	mu       sync.Mutex
	capacity int
	now      func() int64
	items    map[string]*list.Element
	lru      *list.List // front 为最近使用
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

// Put 写入或更新一个键。已存在的键只更新值并提升为最近使用，不刷新 TTL。
func (c *Cache) Put(key, val string, ttlMillis int64) error {
	if ttlMillis <= 0 {
		return ErrInvalidTTL
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if el, ok := c.items[key]; ok {
		el.Value.(*entry).val = val
		c.lru.MoveToFront(el)
		return nil
	}

	if c.lru.Len() >= c.capacity {
		c.evictLocked(c.now())
	}

	now := c.now()
	el := c.lru.PushFront(&entry{
		key:       key,
		val:       val,
		createdAt: now,
		expireAt:  now + ttlMillis,
	})
	c.items[key] = el
	return nil
}

// Get 读取一个键。命中未过期项时提升为最近使用；命中过期项时删除该项并返回 miss。
func (c *Cache) Get(key string) (val string, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	el, ok := c.items[key]
	if !ok {
		return "", false
	}
	e := el.Value.(*entry)
	if c.now() >= e.expireAt {
		c.removeLocked(el)
		return "", false
	}
	c.lru.MoveToFront(el)
	return e.val, true
}

// Delete 删除一个键，返回是否真的删掉了东西（含已过期但未清理的项）。
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

// Len 返回当前缓存中的条目数，包含尚未清理的过期项。
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lru.Len()
}

// removeLocked 从链表和索引中移除一个元素，调用方须已持有锁。
func (c *Cache) removeLocked(el *list.Element) {
	c.lru.Remove(el)
	delete(c.items, el.Value.(*entry).key)
}
