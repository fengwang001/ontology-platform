package payledger

import "container/heap"

// dayHeap 是 int64 天数的最小堆（实现 heap.Interface）。
type dayHeap []int64

func (h dayHeap) Len() int           { return len(h) }
func (h dayHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h dayHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *dayHeap) Push(x any)        { *h = append(*h, x.(int64)) }
func (h *dayHeap) Pop() any {
	old := *h
	n := len(old)
	v := old[n-1]
	*h = old[:n-1]
	return v
}

// holdTracker 按过期日聚合账户的持有，使可用额度查询开销与账户历史
// 授权总数、全部账户数均无关。
//
// 设计：持有按截止日分桶。水位 watermark 记录被评估过的最大时刻。
//   - 活跃桶（day >= watermark）：map + 最小堆，liveTotal 维护其总额。
//   - graveyard（day < watermark）：带子树和的笛卡尔树，支持任意
//     历史时刻 t 的区间求和（查询的 now 可任意，包括早于水位的时刻）。
//
// 前向查询（now >= watermark）把堆顶 day < now 的桶结算进 graveyard，
// 每桶一生只被结算一次，均摊 O(1)；后向查询（now < watermark）在
// graveyard 上做一次 O(log G) 求和。两种查询都不扫描授权记录，
// 也不触碰其他账户。
//
// 不变式：活跃桶的 day >= watermark；graveyard 桶的 day < watermark。
// 查询对内部缓存的结算是语义中性的：结果始终是（已接受操作, now）的纯函数。
type holdTracker struct {
	liveTotal  int64
	buckets    map[int64]int64
	days       dayHeap
	watermark  int64
	hasWM      bool
	graveRoot  *sumNode
	graveTotal int64
}

func newHoldTracker() holdTracker {
	return holdTracker{buckets: make(map[int64]int64)}
}

// add 在截止日 day 上新增 amount 的持有（立即生效）。
func (t *holdTracker) add(day, amount int64) {
	if amount == 0 {
		return
	}
	if t.hasWM && day < t.watermark {
		t.graveRoot = upsert(t.graveRoot, day, amount)
		t.graveTotal += amount
		return
	}
	if _, ok := t.buckets[day]; !ok {
		heap.Push(&t.days, day)
	}
	t.buckets[day] += amount
	t.liveTotal += amount
}

// remove 在截止日 day 上移除 amount 的持有（捕获/撤销/终捕/增量重排）。
func (t *holdTracker) remove(day, amount int64) {
	if amount == 0 {
		return
	}
	if t.hasWM && day < t.watermark {
		t.graveRoot = upsert(t.graveRoot, day, -amount)
		t.graveTotal -= amount
		return
	}
	t.buckets[day] -= amount
	if t.buckets[day] == 0 {
		delete(t.buckets, day) // 堆中残留日为惰性删除，reap 时跳过
	}
	t.liveTotal -= amount
}

// active 返回 now 时刻的有效持有总额。
func (t *holdTracker) active(now int64) int64 {
	if !t.hasWM || now > t.watermark {
		t.reap(now)
	}
	if now >= t.watermark {
		return t.liveTotal
	}
	// 后向查询：活跃桶满足 day >= watermark >= now，全部计入；
	// graveyard 中仅 day >= now 的部分计入。
	return t.liveTotal + t.graveTotal - sumLess(t.graveRoot, now)
}

// reap 把截止日早于 now 的活跃桶结算进 graveyard，并推进水位。
func (t *holdTracker) reap(now int64) {
	for len(t.days) > 0 && t.days[0] < now {
		day := heap.Pop(&t.days).(int64)
		if amt, ok := t.buckets[day]; ok {
			delete(t.buckets, day)
			t.liveTotal -= amt
			t.graveRoot = upsert(t.graveRoot, day, amt)
			t.graveTotal += amt
		}
	}
	if !t.hasWM || now > t.watermark {
		t.watermark = now
		t.hasWM = true
	}
}
