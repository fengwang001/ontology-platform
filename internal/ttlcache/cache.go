package ttlcache

import (
	"container/list"
	"sync"
)

// entry 是缓存中一个键的内部表示。
type entry struct {
	key      string
	val      string
	expireAt int64 // 逻辑毫秒时刻，now() >= expireAt 即视为过期
	writeAt  int64 // 首次写入的逻辑时刻，用于过期项驱逐排序
}

// Cache 是一个容量受限、带 TTL 的 LRU 缓存。
// 时间完全由外部注入的 now 函数提供，实现内部不读取真实时钟。
type Cache struct {
	mu    sync.Mutex
	cap   int
	now   func() int64
	ll    *list.List // 队首 = 最近使用，队尾 = 最久未使用
	items map[string]*list.Element
}

// New 创建一个容量为 capacity 的缓存。now 返回当前逻辑毫秒时刻。
func New(capacity int, now func() int64) (*Cache, error) {
	if capacity <= 0 {
		return nil, ErrInvalidCapacity
	}
	return &Cache{
		cap:   capacity,
		now:   now,
		ll:    list.New(),
		items: make(map[string]*list.Element),
	}, nil
}

// expired 判定「到点即过期」：now >= expireAt 视为过期。
func (c *Cache) expired(e *entry) bool {
	return c.now() >= e.expireAt
}

func (c *Cache) removeElem(el *list.Element) {
	c.ll.Remove(el)
	delete(c.items, el.Value.(*entry).key)
}

// evictOne 在容量满时驱逐一项：
// 优先驱逐已过期项中写入时刻最早者；无过期项则驱逐最久未使用项。
func (c *Cache) evictOne() {
	var oldest *list.Element
	for key, el := range c.items {
		if !c.expired(el.Value.(*entry)) {
			continue
		}
		if oldest == nil ||
			el.Value.(*entry).writeAt < oldest.Value.(*entry).writeAt {
			oldest = c.items[key]
		}
	}
	if oldest != nil {
		c.removeElem(oldest)
		return
	}
	c.removeElem(c.ll.Back())
}

// Put 写入或更新一个键。ttlMillis 必须为正。
// 更新已存在的键时不刷新过期时刻，仅更新值并提升为最近使用。
func (c *Cache) Put(key, val string, ttlMillis int64) error {
	if ttlMillis <= 0 {
		return ErrInvalidTTL
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		el.Value.(*entry).val = val
		c.ll.MoveToFront(el)
		return nil
	}
	if c.ll.Len() >= c.cap {
		c.evictOne()
	}
	now := c.now()
	el := c.ll.PushFront(&entry{
		key:      key,
		val:      val,
		expireAt: now + ttlMillis,
		writeAt:  now,
	})
	c.items[key] = el
	return nil
}

// Get 读取一个键。命中未过期项时提升为最近使用；
// 命中已过期项时返回 miss 并立即删除该项。
func (c *Cache) Get(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return "", false
	}
	e := el.Value.(*entry)
	if c.expired(e) {
		c.removeElem(el)
		return "", false
	}
	c.ll.MoveToFront(el)
	return e.val, true
}

// Delete 删除一个键，返回是否真的删掉了东西。
// 已过期但尚未清理的项也算作被删除。
func (c *Cache) Delete(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return false
	}
	c.removeElem(el)
	return true
}

// Len 返回当前项数，包含尚未清理的过期项。
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}
