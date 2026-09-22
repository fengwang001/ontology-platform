// Package ttlcache 实现一个时间完全由外部注入的带 TTL 的 LRU 缓存。
package ttlcache

import "container/list"

type entry struct {
	key      string
	val      string
	writeAt  int64
	expireAt int64
	node     *tnode // 该项在驱逐索引中的位置
}

// Cache 是带 TTL 的 LRU 缓存，非并发安全。
type Cache struct {
	capacity int
	now      func() int64
	items    map[string]*list.Element
	lru      *list.List // 队首为最近使用

	// 驱逐索引：与 items 内容一致的 treap，使单次驱逐只沿一条
	// 树路径考察候选，不随缓存规模线性增长。
	root     *tnode
	seed     uint64 // 堆优先级伪随机序列状态
	touchSeq int64  // 递减的触碰序号，越小越最近使用

	// lastEvictExamined 记录最近一次驱逐考察的候选节点数。
	lastEvictExamined int
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
		seed:     0x9E3779B97F4A7C15,
	}, nil
}

// Put 写入或更新键值对，ttlMillis 必须为正数。
// 更新已存在的键时不刷新 TTL，过期时刻仍以最初写入为准。
func (c *Cache) Put(key, val string, ttlMillis int64) error {
	if ttlMillis <= 0 {
		return ErrInvalidTTL
	}
	if el, ok := c.items[key]; ok {
		e := el.Value.(*entry)
		e.val = val
		c.lru.MoveToFront(el)
		c.retouch(e)
		return nil
	}
	if len(c.items) >= c.capacity {
		c.evict()
	}
	now := c.now()
	e := &entry{
		key:      key,
		val:      val,
		writeAt:  now,
		expireAt: now + ttlMillis,
	}
	el := c.lru.PushFront(e)
	c.items[key] = el
	c.touchSeq--
	e.node = &tnode{
		el:      el,
		writeAt: now,
		touch:   c.touchSeq,
		exp:     e.expireAt,
		minExp:  e.expireAt,
		prio:    c.nextPrio(),
	}
	c.root = tInsert(c.root, e.node)
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
	c.retouch(e)
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
	c.root = tDelete(c.root, e.node)
}

// retouch 把被触碰的项在驱逐索引中更新为"最近使用"，
// 与 LRU 链表的 MoveToFront 保持同一相对顺序。
func (c *Cache) retouch(e *entry) {
	c.root = tDelete(c.root, e.node)
	c.touchSeq--
	e.node.touch = c.touchSeq
	e.node.left, e.node.right = nil, nil
	e.node.minExp = e.node.exp
	c.root = tInsert(c.root, e.node)
}

// evict 驱逐一项：优先驱逐已过期项中写入时刻最早者；写入时刻并列时
// 取其中最近使用（离 LRU 队首最近）者；没有过期项时驱逐最久未使用的项。
func (c *Cache) evict() {
	if n := c.findVictim(); n != nil {
		c.remove(n.el)
		return
	}
	c.remove(c.lru.Back())
}
