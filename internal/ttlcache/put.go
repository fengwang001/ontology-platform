package ttlcache

import "container/list"

// Put inserts or updates key.
//
// For a new key, the item expires at now+ttlMillis and, if the cache is
// full, one item is evicted first (see evict). For an existing key, only
// the value is updated and the item is promoted to most recently used;
// the original expiry is kept (TTL is NOT refreshed).
func (c *Cache) Put(key, val string, ttlMillis int64) error {
	if ttlMillis <= 0 {
		return ErrInvalidTTL
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.items[key]; ok {
		elem.Value.(*entry).val = val
		c.ll.MoveToFront(elem)
		return nil
	}

	now := c.now()
	if c.ll.Len() >= c.capacity {
		c.evict(now)
	}
	e := &entry{key: key, val: val, createdAt: now, expireAt: now + ttlMillis}
	c.items[key] = c.ll.PushFront(e)
	return nil
}

// evict removes exactly one item, assuming the cache is full.
// If any item is expired, the one with the earliest write time is
// evicted; otherwise the least recently used item is evicted.
func (c *Cache) evict(now int64) {
	var oldest *list.Element
	for elem := c.ll.Front(); elem != nil; elem = elem.Next() {
		e := elem.Value.(*entry)
		if !expired(e, now) {
			continue
		}
		if oldest == nil || e.createdAt < oldest.Value.(*entry).createdAt {
			oldest = elem
		}
	}
	if oldest != nil {
		c.remove(oldest)
		return
	}
	c.remove(c.ll.Back())
}
