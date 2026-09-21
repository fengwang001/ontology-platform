package ttlcache

// pushFront 把 e 放到链表头部（标记为最近使用）。
func (c *Cache) pushFront(e *entry) {
	e.prev = nil
	e.next = c.head
	if c.head != nil {
		c.head.prev = e
	}
	c.head = e
	if c.tail == nil {
		c.tail = e
	}
}

// unlink 把 e 从链表中摘除，但不修改 e 自身的指针。
func (c *Cache) unlink(e *entry) {
	if e.prev != nil {
		e.prev.next = e.next
	} else {
		c.head = e.next
	}
	if e.next != nil {
		e.next.prev = e.prev
	} else {
		c.tail = e.prev
	}
	e.prev = nil
	e.next = nil
}

// moveToFront 把已在链表中的 e 提升为最近使用。
func (c *Cache) moveToFront(e *entry) {
	if c.head == e {
		return
	}
	c.unlink(e)
	c.pushFront(e)
}

// removeEntry 从链表与索引表中删除 e。
func (c *Cache) removeEntry(e *entry) {
	c.unlink(e)
	delete(c.items, e.key)
}
