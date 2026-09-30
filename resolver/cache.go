package resolver

import "sort"

// lookup 返回名字当前有效的缓存条目；不存在或已到期均视为未命中。
// 调用方必须持有 c.mu。
func (c *Cache) lookup(name Name, now int64) (*entry, bool) {
	e, ok := c.store[name]
	if !ok {
		return nil, false
	}
	if !e.alive(now) {
		return nil, false
	}
	return e, true
}

// storeEntry 写入一条记录（同名替换）。ttl 为该记录的存活时间；
// ttl<=0 的记录不存入缓存。存入前先清除失效条目。
// 调用方必须持有 c.mu。
func (c *Cache) storeEntry(name Name, ttl int64, e *entry) {
	if ttl <= 0 || c.capacity <= 0 {
		return
	}
	c.purgeExpiredLocked(c.now())
	if _, exists := c.store[name]; !exists && len(c.store) >= c.capacity {
		c.evict(1)
	}
	c.store[name] = e
}

// evict 先清除全部失效条目，仍不足空间时淘汰到期最早者，
// 到期时刻并列时淘汰名字字典序最小者。调用方必须持有 c.mu。
func (c *Cache) evict(need int) {
	for len(c.store)+need > c.capacity {
		c.purgeExpiredLocked(c.now())
		if len(c.store)+need <= c.capacity {
			return
		}
		victim := c.evictionVictimLocked()
		delete(c.store, victim)
	}
}

// purgeExpiredLocked 删除所有 now 时刻已到期（now >= expireAt）的条目。
func (c *Cache) purgeExpiredLocked(now int64) {
	for name, e := range c.store {
		if !e.alive(now) {
			delete(c.store, name)
		}
	}
}

func (c *Cache) evictionVictimLocked() Name {
	names := make([]Name, 0, len(c.store))
	for name := range c.store {
		names = append(names, name)
	}
	sort.Strings(names)
	victim := names[0]
	earliest := c.store[victim].expireAt
	for _, name := range names[1:] {
		if c.store[name].expireAt < earliest {
			earliest = c.store[name].expireAt
			victim = name
		}
	}
	return victim
}
