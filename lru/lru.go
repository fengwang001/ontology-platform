// Package lru 维护热层键的访问时间（逻辑时钟戳）与 MRU→LRU 顺序，
// 并以 O(1) 定位换出候选。不依赖其他包。
package lru

type entry struct {
	key        string
	ts         int64
	prev, next *entry
}

// LRU 是双向链表 + 索引：表头为 MRU，表尾为 LRU。
type LRU struct {
	items map[string]*entry
	head  *entry // MRU
	tail  *entry // LRU
	// lastChecks 记录最近一次 LruKey() 为定位 LRU 键而检查（比较访问时间）
	// 的热键个数。非导出，不出现在任何公开接口。链表实现下恒为 1。
	lastChecks int
}

func New() *LRU { return &LRU{items: make(map[string]*entry)} }

func (l *LRU) Len() int { return len(l.items) }

func (l *LRU) Has(key string) bool { _, ok := l.items[key]; return ok }

// Add 把新键放到 MRU 位。调用方保证键不存在。
func (l *LRU) Add(key string, ts int64) {
	e := &entry{key: key, ts: ts}
	l.items[key] = e
	l.pushFront(e)
}

// Touch 把已存在的键移到 MRU 并刷新访问时间。调用方保证键存在。
func (l *LRU) Touch(key string, ts int64) {
	e := l.items[key]
	e.ts = ts
	l.detach(e)
	l.pushFront(e)
}

// Remove 摘除一个键（换出时调用）。调用方保证键存在。
func (l *LRU) Remove(key string) {
	e := l.items[key]
	l.detach(e)
	delete(l.items, key)
}

// LruKey 定位换出候选：访问时间最早的键，即表尾，O(1)。
// 同时把本次检查的热键个数记入 lastChecks。
func (l *LRU) LruKey() (string, bool) {
	if l.tail == nil {
		l.lastChecks = 0
		return "", false
	}
	l.lastChecks = 1 // 只检查表尾一个键，不随热层规模增长
	return l.tail.key, true
}

// Keys 按 MRU→LRU 顺序返回全部热键。
func (l *LRU) Keys() []string {
	out := make([]string, 0, len(l.items))
	for e := l.head; e != nil; e = e.next {
		out = append(out, e.key)
	}
	return out
}

func (l *LRU) pushFront(e *entry) {
	e.prev, e.next = nil, l.head
	if l.head != nil {
		l.head.prev = e
	} else {
		l.tail = e
	}
	l.head = e
}

func (l *LRU) detach(e *entry) {
	if e.prev != nil {
		e.prev.next = e.next
	} else {
		l.head = e.next
	}
	if e.next != nil {
		e.next.prev = e.prev
	} else {
		l.tail = e.prev
	}
	e.prev, e.next = nil, nil
}
