package whiteboard

// lock 表示一把软锁。锁在 expireAt 时刻到期，恰等于到期时刻视为已到期。
type lock struct {
	owner    string
	expireAt int64
}

// lockTable 是标识（元素或组合）到锁的映射。
// 加锁、解锁、查询均为 O(1) 哈希表操作，
// 不随锁的总数或历史修订数增长；到期锁在下次访问时惰性清理。
type lockTable struct {
	m map[string]lock
}

func newLockTable() *lockTable {
	return &lockTable{m: make(map[string]lock)}
}

// active 返回 id 上在 now 时刻仍未到期的锁；没有则 ok=false。
// 恰等于到期时刻（now == expireAt）视为已到期。
func (lt *lockTable) active(id string, now int64) (lock, bool) {
	l, ok := lt.m[id]
	if !ok {
		return lock{}, false
	}
	if l.expireAt <= now {
		delete(lt.m, id) // 惰性清理已到期锁
		return lock{}, false
	}
	return l, true
}

func (lt *lockTable) set(id, owner string, expireAt int64) {
	lt.m[id] = lock{owner: owner, expireAt: expireAt}
}

func (lt *lockTable) del(id string) {
	delete(lt.m, id)
}
