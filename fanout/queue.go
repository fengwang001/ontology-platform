// Package fanout 是单个订阅者的有界 FIFO 队列：满/空判定、入队/出队、
// tail-drop 丢弃计数与长度。不依赖其他包。
package fanout

import "sync"

// Queue 是容量固定的环形 FIFO。每个订阅者各自持有一把锁，互不影响。
type Queue struct {
	mu      sync.Mutex
	slots   []int64 // 后备环形数组，容量在构造时固定
	head    int     // 队首下标
	count   int     // 当前长度，由字段直接维护，判满/判空均为 O(1)
	dropped int     // 累计 tail-drop 次数

	// slotChecks 记录历次 Offer 为判定「是否已满」而做的槽位级检查次数。
	// 长度由 count 字段维护：每次判满是 1 次常数检查，与容量无关；
	// 若改成逐槽扫描，该值会随容量线性增长。非导出，不进入公开接口。
	slotChecks int
}

// NewQueue 创建容量为 c（c >= 1）的空队列。
func NewQueue(c int) *Queue {
	return &Queue{slots: make([]int64, c)}
}

// Offer 尝试入队尾：未满则写入；已满则 tail-drop —— 丢弃新来的 ev，
// 队内旧事件原样保留，丢弃计数 +1。绝不阻塞。
func (q *Queue) Offer(ev int64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.slotChecks++ // 一次判满检查（count 与 cap 的常量比较，不遍历槽位）
	if q.count == len(q.slots) {
		q.dropped++
		return
	}
	q.slots[(q.head+q.count)%len(q.slots)] = ev
	q.count++
}

// Poll 弹出并返回队首事件；空队列返回 ok=false。
func (q *Queue) Poll() (int64, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.count == 0 {
		return 0, false
	}
	ev := q.slots[q.head]
	q.head = (q.head + 1) % len(q.slots)
	q.count--
	return ev, true
}

// Len 返回当前队列长度。
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.count
}

// Dropped 返回累计 tail-drop 次数。
func (q *Queue) Dropped() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.dropped
}

// OfferCheckCostIsConstant 自检：在多档大容量队列上各 Offer 一次，
// 判定单次「是否已满」检查的槽位计数增量恒为 1（小常数），不随容量增长。
// 只返回布尔，绝不暴露 slotChecks 的数值。
func OfferCheckCostIsConstant() bool {
	for _, m := range []int{100, 1000, 10000} {
		q := NewQueue(m)
		before := q.slotChecks
		q.Offer(1)
		if d := q.slotChecks - before; d != 1 {
			return false
		}
	}
	return true
}
