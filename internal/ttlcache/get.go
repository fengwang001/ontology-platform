package ttlcache

// Get looks up key. On a hit the item is promoted to most recently used.
// A hit on an expired item is a miss: the item is removed immediately.
func (c *Cache) Get(key string) (val string, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.items[key]
	if !ok {
		return "", false
	}
	e := elem.Value.(*entry)
	if expired(e, c.now()) {
		c.remove(elem)
		return "", false
	}
	c.ll.MoveToFront(elem)
	return e.val, true
}

// Delete removes key and reports whether anything was removed.
// Removing an expired but not yet cleaned-up item counts as a delete.
func (c *Cache) Delete(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.items[key]
	if !ok {
		return false
	}
	c.remove(elem)
	return true
}

// Len returns the number of items, including expired ones that have
// not been cleaned up yet.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}
