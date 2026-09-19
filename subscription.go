package ontology

type subscriber struct {
	id       string
	prefix   string
	props    map[string]struct{}
	ch       chan Message
	overflow OverflowPolicy
	pending  PendingPolicy

	dropped     uint64
	lastDropSeq uint64
	lagged      bool
	closed      bool
}

// Subscription 是返回给调用方的订阅句柄。
type Subscription struct {
	sub *subscriber
	d   *Dispatcher
}

// C 返回订阅者自己的有界消息通道。DrainPending 收尾时通道会在读完后关闭。
func (s *Subscription) C() <-chan Message { return s.sub.ch }

// Stats 返回当前计量快照；订阅终止后仍可通过句柄查到最终累计值。
func (s *Subscription) Stats() Stats {
	s.d.mu.Lock()
	defer s.d.mu.Unlock()
	sub := s.sub
	return Stats{
		Dropped:     sub.dropped,
		LastDropSeq: sub.lastDropSeq,
		Lagged:      sub.lagged,
		Closed:      sub.closed,
	}
}

// Unsubscribe 幂等取消：重复调用安全；取消后不会再收到任何新消息。
func (s *Subscription) Unsubscribe() { s.d.Unsubscribe(s.sub.id) }

// matches 判断该订阅者是否关心 (entity, property)。
func (sub *subscriber) matches(entity, property string) bool {
	return entityMatches(entity, sub.prefix) && propertyMatches(property, sub.props)
}

// recordDrop 记录一次可归因丢弃。
func (sub *subscriber) recordDrop(seq uint64) {
	sub.dropped++
	sub.lastDropSeq = seq
}

// deliver 非阻塞投递一条消息。任何情况下都不等待：
// 队列满时按 overflow 策略独立处置，绝不影响其他订阅者。
// 返回 false 表示该订阅者应被终止（落后断开）。
func (sub *subscriber) deliver(m Message) bool {
	if sub.closed || sub.lagged {
		return true
	}
	select {
	case sub.ch <- m:
		return true
	default:
	}
	switch sub.overflow {
	case DropOldest:
		// 队列已满：弹出最旧一条并计入丢弃，再非阻塞写入新消息。
		select {
		case old := <-sub.ch:
			sub.recordDrop(old.Seq)
		default:
		}
		select {
		case sub.ch <- m:
		default:
			// 极端竞态下仍写不进：按丢弃新消息兜底，绝不阻塞。
			sub.recordDrop(m.Seq)
		}
		return true
	case DropNewest:
		sub.recordDrop(m.Seq)
		return true
	default: // DisconnectLagging
		sub.recordDrop(m.Seq)
		sub.lagged = true
		return false
	}
}

// teardown 终止订阅。
// 因落后而断开时：保留队列中已有消息供调用方读完（通道不关闭）。
// 因取消/关闭而终止时：按 pending 策略收尾（DropPending 清空并关闭，DrainPending 保留）。
func (sub *subscriber) teardown(policy PendingPolicy, lagged bool) {
	if sub.closed {
		return
	}
	sub.closed = true
	sub.lagged = sub.lagged || lagged
	if lagged {
		return
	}
	if policy == DropPending {
		drainCount(sub)
		close(sub.ch)
	}
}

// drainCount 统计并清空队列中尚未取出的残留消息。
func drainCount(sub *subscriber) {
	var last uint64
	var n uint64
	for {
		select {
		case m := <-sub.ch:
			n++
			last = m.Seq
		default:
			sub.dropped += n
			if n > 0 {
				sub.lastDropSeq = last
			}
			return
		}
	}
}
