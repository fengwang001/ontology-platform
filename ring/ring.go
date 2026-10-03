// Package ring 实现定长 N 的计数滑窗（覆盖写环形缓冲，计数增量维护）。
package ring

// Entry 是环中的一次调用结算记录。
type Entry struct {
	Failed bool
	Slow   bool
}

// Ring 是计数滑窗。零值不可用，须用 New 构造。
//
// 底层为覆盖写环形数组：head 为最旧元素下标，size 为当前条数，
// size<n 时写入 head+size，否则覆盖 head 并前移。
// 条数、失败数、慢数均增量维护，任何操作考察的槽位为常数：
// touches 记录被触碰（读/写）的槽位次数，供测试验证与 n 无关。
type Ring struct {
	slots   []Entry
	head    int
	size    int
	failed  int
	slow    int
	touches int
}

// New 创建容量为 n 的环（n>=1）。
func New(n int) *Ring {
	return &Ring{slots: make([]Entry, n)}
}

// Add 追加一条记录，环满时淘汰最旧记录并返回被淘汰项与 true。
func (r *Ring) Add(e Entry) (Entry, bool) {
	var evicted Entry
	dropped := false
	if r.size == len(r.slots) {
		evicted = r.slots[r.head]
		r.touches++
		if evicted.Failed {
			r.failed--
		}
		if evicted.Slow {
			r.slow--
		}
		r.slots[r.head] = e
		r.touches++
		r.head = (r.head + 1) % len(r.slots)
		dropped = true
	} else {
		idx := (r.head + r.size) % len(r.slots)
		r.slots[idx] = e
		r.touches++
		r.size++
	}
	if e.Failed {
		r.failed++
	}
	if e.Slow {
		r.slow++
	}
	return evicted, dropped
}

// Reset 清空环。
func (r *Ring) Reset() {
	r.head = 0
	r.size = 0
	r.failed = 0
	r.slow = 0
	r.touches = 0
}

// Count 返回环内条数。
func (r *Ring) Count() int { return r.size }

// Failed 返回环内失败数。
func (r *Ring) Failed() int { return r.failed }

// Slow 返回环内慢调用数。
func (r *Ring) Slow() int { return r.slow }

// Touches 返回累计触碰槽位次数（非导出计数的测试访问点）：
// 每次 Add 至多触碰 2 个槽位（满时读旧槽+写同槽；非满只写 1 个槽位），
// 因此总触碰次数只随操作数线性增长，与容量 n 无关。
func (r *Ring) Touches() int { return r.touches }
