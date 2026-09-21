
package ttlcache

// Cache 是带 TTL 的 LRU 缓存。
//
// 时间完全由构造时注入的 now 函数提供（单位：毫秒），
// 实现内部不调用 time.Now。
type Cache struct {
	capacity int
	now      func() int64

	items map[string]*entry
	head  *entry // 最近使用
	tail  *entry // 最久未使用
}

// New 创建容量为 capacity 的缓存；now 返回当前逻辑时刻（毫秒）。
func New(capacity int, now func() int64) (*Cache, error) {
	if capacity <= 0 {
		return nil, ErrInvalidCapacity
	}
	if now == nil {
		return nil, ErrNilClock
	}
	return &Cache{
		capacity: capacity,
		now:      now,
		items:    make(map[string]*entry, capacity),
	}, nil
}

// Len 返回当前条目数，包含尚未被清理的过期项。
func (c *Cache) Len() int {
	return len(c.items)
}

// evict 在容量不足时驱逐一个条目：
// 优先驱逐写入时刻最早的过期项；没有过期项时驱逐最久未使用项。
func (c *Cache) evict(now int64) {
	var oldestExpired *entry
	for e := c.head; e != nil; e = e.next {
		if e.expiredAt(now) && (oldestExpired == nil || e.createdAt < oldestExpired.createdAt) {
			oldestExpired = e
		}
	}
	if oldestExpired != nil {
		c.removeEntry(oldestExpired)
		return
	}
	if c.tail != nil {
		c.removeEntry(c.tail)
	}
}
