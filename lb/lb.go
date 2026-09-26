// Package lb 漏桶核心：出发时间 FIFO 队列（队头最早），不依赖其他包。
package lb

// Bucket 漏桶。调用方保证参数合法（capacity≥1、interval≥1）且时钟单调。
type Bucket struct {
	capacity int64
	interval int64
	q        []int64 // 出发时间队列，q[head] 为队头
	head     int
	last     int64 // 上一项出发时间
	// drainChecks 记录最近一次 Drain 探测过的队列项数，仅包内测试可读。
	drainChecks int
}

// New 构造漏桶。
func New(capacity, interval int64) *Bucket {
	return &Bucket{capacity: capacity, interval: interval}
}

// Drain 移除所有出发时间 ≤ t 的项；只看队头，遇到未出发项即止。
func (b *Bucket) Drain(t int64) {
	b.drainChecks = 0
	for b.head < len(b.q) {
		b.drainChecks++
		if b.q[b.head] > t {
			break
		}
		b.head++
	}
	if b.head == len(b.q) { // 已空则回收底层数组
		b.q = b.q[:0]
		b.head = 0
	}
}

// Admit 判满并接纳：满返回 full=true 且不改状态；否则计算出发时间并入队。
func (b *Bucket) Admit(t int64) (dep int64, full bool) {
	if int64(b.InSystem()) >= b.capacity {
		return 0, true
	}
	dep = t + b.interval
	if b.InSystem() > 0 {
		dep = b.last + b.interval
	}
	b.last = dep
	b.q = append(b.q, dep)
	return dep, false
}

// InSystem 返回系统内（未漏出）项数。
func (b *Bucket) InSystem() int {
	return len(b.q) - b.head
}
