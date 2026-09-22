// Package ttlcache 实现一个时间完全由外部注入的带 TTL 的 LRU 缓存。
package ttlcache

import "container/list"

type entry struct {
	key      string
	val      string
	writeAt  int64
	expireAt int64
}

// Cache 是带 TTL 的 LRU 缓存，非并发安全。
type Cache struct {
	capacity int
	now      func() int64
	items    map[string]*list.Element
	lru      *list.List // 队首为最近使用
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
		el.Value.(*entry).val = val
		c.lru.MoveToFront(el)
		return nil
	}
	if len(c.items) >= c.capacity {
		c.evict()
	}
	now := c.now()
	el := c.lru.PushFront(&entry{
		key:      key,
		val:      val,
		writeAt:  now,
		expireAt: now + ttlMillis,
	})
	c.items[key] = el
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
}

// evict 驱逐一项：优先驱逐已过期项中写入时刻最早者，
// 没有过期项时驱逐最久未使用的项。
func (c *Cache) evict() {
	var oldest *list.Element
	for el := c.lru.Front(); el != nil; el = el.Next() {
		e := el.Value.(*entry)
		if !c.expired(e) {
			continue
		}
		if oldest == nil || e.writeAt < oldest.Value.(*entry).writeAt {
			oldest = el
		}
	}
	if oldest != nil {
		c.remove(oldest)
		return
	}
	c.remove(c.lru.Back())
}
