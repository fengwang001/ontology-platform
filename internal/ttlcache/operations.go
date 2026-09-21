
package ttlcache

// Put 写入键值。重复 Put 已存在的键只更新值并提升为最近使用，
// 不刷新 TTL（过期时刻以首次写入为准）。ttlMillis 必须为正数。
func (c *Cache) Put(key, val string, ttlMillis int64) error {
	if ttlMillis <= 0 {
		return ErrInvalidTTL
	}
	now := c.now()

	if e, ok := c.items[key]; ok {
		e.val = val
		c.moveToFront(e)
		return nil
	}

	if len(c.items) >= c.capacity {
		c.evict(now)
	}

	e := &entry{
		key:       key,
		val:       val,
		createdAt: now,
		ttl:       ttlMillis,
	}
	c.items[key] = e
	c.pushFront(e)
	return nil
}

// Get 读取键。命中已过期项时按 miss 处理并立即删除该项；
// 命中未过期项时把它提升为最近使用。
func (c *Cache) Get(key string) (string, bool) {
	e, ok := c.items[key]
	if !ok {
		return "", false
	}
	if e.expiredAt(c.now()) {
		c.removeEntry(e)
	return "", false
	}
	c.moveToFront(e)
	return e.val, true
}

// Delete 删除键；删除一个已过期但尚未清理的项同样返回 true。
func (c *Cache) Delete(key string) bool {
	e, ok := c.items[key]
	if !ok {
		return false
	}
	c.removeEntry(e)
	return true
}
