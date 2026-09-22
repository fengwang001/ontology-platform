package bus

// flushLocked 必须在持锁状态下调用，向所有订阅者投递一条通知。
func (b *Bus) flushLocked(n Notice) {
	for _, fn := range b.subs {
		fn(n)
		b.stats.Delivered++
	}
}

// DeliverAll 顺序投递并清空待投递队列。
func (b *Bus) DeliverAll() {
	b.mu.Lock()
	q := b.pending
	b.pending = nil
	for _, n := range q {
		b.flushLocked(n)
	}
	b.mu.Unlock()
}

// DeliverShuffled 随机打乱投递顺序（Fischer–Yates，使用注入的随机源）。
func (b *Bus) DeliverShuffled() {
	b.mu.Lock()
	q := b.pending
	b.pending = nil
	for i := len(q) - 1; i > 0; i-- {
		j := int(b.rng.Int63n(int64(i + 1)))
		q[i], q[j] = q[j], q[i]
	}
	for _, n := range q {
		b.flushLocked(n)
	}
	b.mu.Unlock()
}

// DeliverDuplicated 投递队列中的每条通知，每条额外重复 repeat 次
// （即每条共投递 repeat+1 份）；重复件紧跟原件之后。
func (b *Bus) DeliverDuplicated(repeat int) {
	b.mu.Lock()
	q := b.pending
	b.pending = nil
	for _, n := range q {
		b.flushLocked(n)
		for k := 0; k < repeat; k++ {
			b.flushLocked(n)
		}
	}
	b.mu.Unlock()
}

// DeliverWithLoss 每条通知以 dropProb 概率被丢弃（不投递），其余顺序投递。
func (b *Bus) DeliverWithLoss(dropProb float64) {
	b.mu.Lock()
	q := b.pending
	b.pending = nil
	for _, n := range q {
		if b.rng.Float64() < dropProb {
			b.stats.Dropped++
			continue
		}
		b.flushLocked(n)
	}
	b.mu.Unlock()
}
