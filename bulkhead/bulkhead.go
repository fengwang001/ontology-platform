// Package bulkhead 实现并发许可与有界 FIFO 等待队列。
package bulkhead

// Canceller 由 guard 实现：id 离队（超时/撤销）时给出最终结局回调。
type Canceller func(id int64)

type waiter struct {
	id int64
	at int64
}

// Bulkhead 为舱壁。非并发安全，由 guard 持锁串行调用。
type Bulkhead struct {
	permits int
	active  int
	capQ    int
	queue   []waiter
}

// New 创建容量为 permits、队列容量为 queue 的舱壁。
func New(permits, queue int) *Bulkhead {
	return &Bulkhead{permits: permits, capQ: queue}
}

// SettleTimeouts 使 at+wt<=now 的等待者按入队序出队超时，对每个超时 id 调用 timeout。
func (b *Bulkhead) SettleTimeouts(now int64, wt int64, timeout Canceller) {
	i := 0
	for i < len(b.queue) {
		w := b.queue[i]
		if w.at+wt > now {
			break
		}
		timeout(w.id)
		i++
	}
	if i > 0 {
		b.queue = b.queue[i:]
	}
}

// Free 返回当前空闲许可数。
func (b *Bulkhead) Free() int { return b.permits - b.active }

// Acquire 占用一个空闲许可（调用前须保证 Free()>0）。
func (b *Bulkhead) Acquire() { b.active++ }

// Release 归还一个许可。
func (b *Bulkhead) Release() { b.active-- }

// QueueLen 返回队列长度。
func (b *Bulkhead) QueueLen() int { return len(b.queue) }

// QueueCap 返回队列容量。
func (b *Bulkhead) QueueCap() int { return b.capQ }

// Active 返回当前在役许可数。
func (b *Bulkhead) Active() int { return b.active }

// Enqueue 入队一个等待者（调用前须保证 QueueLen()<capQ）。
func (b *Bulkhead) Enqueue(id int64, now int64) {
	b.queue = append(b.queue, waiter{id: id, at: now})
}

// TryFill 在仍为 Closed 时按 FIFO 授予空闲许可（占用许可、出队），
// 每个授予的 id 调用 grant；一旦状态不再 Closed 立即停止（此时队列必已为空）。
func (b *Bulkhead) TryFill(now int64, stillClosed func() bool, grant func(id int64)) {
	_ = now
	for b.Free() > 0 && len(b.queue) > 0 {
		if !stillClosed() {
			return
		}
		w := b.queue[0]
		b.queue = b.queue[1:]
		b.active++
		grant(w.id)
	}
}

// CancelAll 使全部等待者按入队序出队撤销，每个 id 调用 cancel。
func (b *Bulkhead) CancelAll(cancel Canceller) {
	for _, w := range b.queue {
		cancel(w.id)
	}
	b.queue = b.queue[:0]
}
