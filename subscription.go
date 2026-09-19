package ontology

import (
	"strings"
	"sync"
	"sync/atomic"
)

// Subscription 表示一个订阅。通过 C() 接收消息，
// 通过 Cancel 取消。取消后不得再收到任何消息。
type Subscription struct {
	id   uint64
	disp *Dispatcher

	prefix string
	props  map[string]struct{}

	overflow OverflowPolicy
	drain    DrainPolicy

	queue chan Message

	mu       sync.Mutex // 保护 closed 与对 queue 的关闭/清空
	canceled atomic.Bool
	closed   bool // queue 是否已关闭

	dropped        atomic.Uint64
	lastDroppedSeq atomic.Uint64
}

// ID 返回订阅者的唯一标识。
func (s *Subscription) ID() uint64 { return s.id }

// C 返回接收消息的只读通道。订阅终止（取消、断开或分发器关闭）
// 后通道会被关闭；DrainPending 策略下剩余消息可先读完。
func (s *Subscription) C() <-chan Message { return s.queue }

// Dropped 返回该订阅者累计丢弃的消息条数。
func (s *Subscription) Dropped() uint64 { return s.dropped.Load() }

// LastDroppedSeq 返回最后一次被丢弃消息的序号；从未丢弃时返回 0。
func (s *Subscription) LastDroppedSeq() uint64 { return s.lastDroppedSeq.Load() }

// Canceled 报告订阅是否已终止（取消、落后断开或分发器关闭）。
func (s *Subscription) Canceled() bool { return s.canceled.Load() }

// matches 报告消息是否匹配本订阅：实体前缀匹配（非子串），
// 属性名集合为空时匹配所有属性，否则要求属性名全等。
func (s *Subscription) matches(entity, property string) bool {
	if !strings.HasPrefix(entity, s.prefix) {
		return false
	}
	if len(s.props) == 0 {
		return true
	}
	_, ok := s.props[property]
	return ok
}

// offer 尝试非阻塞投递。调用时须持有分发器锁，
// 因此同一订阅者的 offer 调用是串行的。
func (s *Subscription) offer(m Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	select {
	case s.queue <- m:
		return
	default:
	}
	// 队列已满，按声明的溢出策略处置。
	s.dropped.Add(1)
	switch s.overflow {
	case DropOldest:
		old := <-s.queue
		s.lastDroppedSeq.Store(old.Seq)
		s.queue <- m
	case DropNewest:
		s.lastDroppedSeq.Store(m.Seq)
	case Disconnect:
		s.lastDroppedSeq.Store(m.Seq)
		s.closeLocked()
	}
}

// Cancel 取消订阅，幂等。返回后不会再有新消息入队；
// 队列中尚未取出的消息按 DrainPolicy 处理。
func (s *Subscription) Cancel() {
	if s.disp != nil {
		s.disp.remove(s.id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if s.drain == DrainPending {
		s.closeLocked()
		return
	}
	// DropPending：丢弃队列中尚未取出的消息再关闭。
	for {
		select {
		case <-s.queue:
		default:
			s.closeLocked()
			return
		}
	}
}

// closeLocked 关闭队列通道，调用时须持有 s.mu。
// 关闭后通道中剩余的消息仍可按 DrainPending 语义被读完。
func (s *Subscription) closeLocked() {
	if s.closed {
		return
	}
	s.closed = true
	s.canceled.Store(true)
	close(s.queue)
}
