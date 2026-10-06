package stackmgr

import "sync"

// quotaAccountant 同时承担两件事：
//  1. 全局总配额（槽位和）的精确扣减与归还；
//  2. 模拟内存申请：bump-pointer arena，arenaOff 随每次成功搬迁单调增长，
//     并可注入“接下来 k 次申请失败”，用于验证搬迁失败时原栈完好。
type quotaAccountant struct {
	mu      sync.Mutex
	used    int
	total   int
	arenaHi int
	failN   int
}

func newQuotaAccountant(total int) *quotaAccountant {
	return &quotaAccountant{total: total}
}

// tryReserveLocked 要求调用方已持有 q.mu（搬迁原子段使用）。
func (q *quotaAccountant) tryReserveLocked(delta int) bool {
	if delta <= 0 {
		return false
	}
	if q.used+delta > q.total {
		return false
	}
	q.used += delta
	return true
}

// release 归还配额；归还次数与扣减严格配对，重复归还会被上层锁序杜绝。
func (q *quotaAccountant) release(delta int) {
	q.mu.Lock()
	q.releaseLocked(delta)
	q.mu.Unlock()
}

// releaseLocked 要求调用方已持有 q.mu。
func (q *quotaAccountant) releaseLocked(delta int) {
	q.used -= delta
	if q.used < 0 {
		q.used = 0
	}
}

// allocLocked 要求调用方已持有 q.mu（搬迁原子段使用）。
func (q *quotaAccountant) allocLocked(slots int) (int, bool) {
	if q.failN > 0 {
		q.failN--
		return 0, false
	}
	// arena 是无限 bump 分配器：容量约束只由 tryReserve 的配额记账负责，
	// 避免把“已扣配额”误判成“无内存”。
	base := q.arenaHi
	q.arenaHi += slots
	return base, true
}

// failNext 使接下来 n 次 alloc 失败（测试用）。
func (q *quotaAccountant) failNext(n int) {
	q.mu.Lock()
	q.failN = n
	q.mu.Unlock()
}

func (q *quotaAccountant) snapshot() (used, total int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.used, q.total
}
