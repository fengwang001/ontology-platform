package ttlcache

// entry 是缓存底层双向链表的节点。
//
// 链表头部为最近使用（MRU），尾部为最久未使用（LRU）。
// createdAt 与 ttl 在键首次写入时确定；重复 Put 已存在的键
// 不会刷新它们。
type entry struct {
	key       string
	val       string
	createdAt int64
	ttl       int64
	prev      *entry
	next      *entry
}

// expireAt 返回「到点即过期」的时刻：该时刻读取已算过期。
func (e *entry) expireAt() int64 {
	return e.createdAt + e.ttl
}

// expiredAt 判断在逻辑时刻 now 该项是否已过期。
func (e *entry) expiredAt(now int64) bool {
	return now >= e.expireAt()
}
