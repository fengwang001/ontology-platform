package dispatch

import "sync"

// Subscription is a single subscriber's handle. Use C to receive messages,
// Cancel to unsubscribe. All methods are safe for concurrent use.
type Subscription struct {
	id    int
	opts  Options
	attrs map[string]struct{}
	ch    chan Message

	mu             sync.Mutex
	closed         bool // no further enqueues; channel closed
	lagging        bool // disconnected because the queue overflowed
	dropped        uint64
	lastDroppedSeq uint64
}

func newSubscription(id int, opts Options) *Subscription {
	return &Subscription{
		id:    id,
		opts:  opts,
		attrs: attrSet(opts.Attrs),
		ch:    make(chan Message, opts.Capacity),
	}
}

// ID returns the subscription's registration order, starting at 0. IDs are
// strictly increasing in Subscribe call order, which makes Matches output
// stable.
func (s *Subscription) ID() int { return s.id }

// C returns the receive channel. It is closed after Cancel, after a
// Disconnect-policy overflow, or when the dispatcher is closed; depending on
// the declared PendingPolicy, remaining queued messages may still be read
// before the close is observed.
func (s *Subscription) C() <-chan Message { return s.ch }

// matches reports whether the message should be delivered to this
// subscription. Pure match criteria only; independent of lifecycle state.
func (s *Subscription) matches(entity, attr string) bool {
	return matchEntity(s.opts.EntityPrefix, entity) && matchAttr(s.attrs, attr)
}

// enqueue delivers m to the queue without ever blocking, applying the
// declared FullPolicy when the queue is full. Called by Publish while the
// dispatcher mutex is held, so calls are fully serialized per dispatcher.
func (s *Subscription) enqueue(m Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	switch s.opts.OnFull {
	case DropOldest:
		select {
		case s.ch <- m:
		default:
			old := <-s.ch
			s.recordDropLocked(old.Seq)
			s.ch <- m
		}
	case DropNewest:
		select {
		case s.ch <- m:
		default:
			s.recordDropLocked(m.Seq)
		}
	case Disconnect:
		select {
		case s.ch <- m:
		default:
			s.recordDropLocked(m.Seq)
			s.lagging = true
			s.closeLocked(DiscardPending)
		}
	}
}

// Cancel unsubscribes. It is idempotent: repeated calls are no-ops and never
// panic. After Cancel returns, no new messages are enqueued; messages still
// queued are handled according to the declared PendingPolicy.
func (s *Subscription) Cancel() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closeLocked(s.opts.OnCancel)
}

// finish is called by Dispatcher.Close; it behaves exactly like Cancel.
func (s *Subscription) finish() { s.Cancel() }

// closeLocked closes the subscription, applying pending to queued messages.
func (s *Subscription) closeLocked(pending PendingPolicy) {
	s.closed = true
	if pending == DiscardPending {
		for {
			select {
			case m := <-s.ch:
				s.recordDropLocked(m.Seq)
			default:
				close(s.ch)
				return
			}
		}
	}
	close(s.ch)
}

func (s *Subscription) recordDropLocked(seq uint64) {
	s.dropped++
	s.lastDroppedSeq = seq
}

// Dropped returns the total number of matched messages this subscriber
// failed to receive (queue-full drops plus pending discards).
func (s *Subscription) Dropped() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dropped
}

// LastDroppedSeq returns the Seq of the most recently dropped message, or 0
// if nothing has been dropped.
func (s *Subscription) LastDroppedSeq() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastDroppedSeq
}

// Lagging reports whether the subscription was disconnected because its
// queue overflowed under the Disconnect policy.
func (s *Subscription) Lagging() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lagging
}

// active reports whether the subscription can still receive messages.
func (s *Subscription) active() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.closed
}
