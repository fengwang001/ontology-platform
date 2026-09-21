package ttlcache

// evictLocked 在容量已满时驱逐一个条目，调用方须已持有锁。
// 优先驱逐已过期项中「写入时刻最早」者；无过期项时驱逐最久未使用项。
func (c *Cache) evictLocked(now int64) {
	var oldestExpired *entry
	for el := c.lru.Front(); el != nil; el = el.Next() {
		e := el.Value.(*entry)
		if now < e.expireAt {
			continue
		}
		if oldestExpired == nil || e.createdAt < oldestExpired.createdAt {
			oldestExpired = e
		}
	}
	if oldestExpired != nil {
		c.removeLocked(c.items[oldestExpired.key])
		return
	}
	c.removeLocked(c.lru.Back())
}
