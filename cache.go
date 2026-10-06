package blockstore

type lruCache struct {
	capacity int
	items    map[int]*cacheEntry
	oldest   *cacheEntry
	newest   *cacheEntry
}

type cacheEntry struct {
	index int
	data  []byte
	older *cacheEntry
	newer *cacheEntry
}

func newLRUCache(capacity int) *lruCache {
	if capacity < 0 {
		capacity = 0
	}
	return &lruCache{capacity: capacity, items: make(map[int]*cacheEntry)}
}

func (c *lruCache) get(index int) ([]byte, bool) {
	if c.capacity == 0 {
		return nil, false
	}
	entry, ok := c.items[index]
	if !ok {
		return nil, false
	}
	c.touch(entry)
	return entry.data, true
}

func (c *lruCache) peek(index int) ([]byte, bool) {
	if c.capacity == 0 {
		return nil, false
	}
	entry, ok := c.items[index]
	if !ok {
		return nil, false
	}
	return entry.data, true
}

func (c *lruCache) put(index int, data []byte) {
	if c.capacity == 0 {
		return
	}
	if entry, ok := c.items[index]; ok {
		entry.data = data
		c.touch(entry)
		return
	}
	for len(c.items) >= c.capacity {
		c.removeOldest()
	}
	entry := &cacheEntry{index: index, data: data}
	c.items[index] = entry
	if c.newest == nil {
		c.oldest = entry
		c.newest = entry
		return
	}
	entry.older = c.newest
	c.newest.newer = entry
	c.newest = entry
}

func (c *lruCache) replace(index int, data []byte) bool {
	entry, ok := c.items[index]
	if !ok {
		return false
	}
	entry.data = data
	return true
}

func (c *lruCache) remove(index int) {
	entry, ok := c.items[index]
	if !ok {
		return
	}
	c.unlink(entry)
	delete(c.items, index)
}

func (c *lruCache) touch(entry *cacheEntry) {
	if entry == c.newest {
		return
	}
	c.unlink(entry)
	entry.older = c.newest
	entry.newer = nil
	if c.newest != nil {
		c.newest.newer = entry
	}
	c.newest = entry
	if c.oldest == nil {
		c.oldest = entry
	}
}

func (c *lruCache) removeOldest() {
	if c.oldest == nil {
		return
	}
	entry := c.oldest
	c.unlink(entry)
	delete(c.items, entry.index)
}

func (c *lruCache) unlink(entry *cacheEntry) {
	if entry.older != nil {
		entry.older.newer = entry.newer
	} else {
		c.oldest = entry.newer
	}
	if entry.newer != nil {
		entry.newer.older = entry.older
	} else {
		c.newest = entry.older
	}
	entry.older = nil
	entry.newer = nil
}
