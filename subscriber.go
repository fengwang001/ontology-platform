package ontology

// subscriber is the dispatcher-internal state of one subscription.
//
// Every field is guarded by Dispatcher.mu. The notify channel is used only
// as a wake-up edge: a producer closes the current channel under mu and a
// waiting consumer replaces it with a fresh channel while still under mu,
// so no signal can ever be lost.
type subscriber struct {
	id      int64
	matcher matcher
	queue   *ring
	policy  OverflowPolicy

	// drainRemaining controls unsubscribe semantics: when true the consumer
	// may finish reading buffered events before receiving ErrDrained; when
	// false the buffer is discarded immediately.
	drainRemaining bool

	// active means the subscriber still participates in fan-out. It becomes
	// false on overflow-with-Disconnect, on unsubscribe, and on Close.
	active bool
	// removed means the subscriber is no longer registered with the
	// dispatcher (unsubscribed or dispatcher closed).
	removed bool

	notify  chan struct{}
	enqueued int64
	offered  int64
	dropped  int64
	lastDrop int64
}

// terminalError returns the error Next must report once the buffer is empty,
// given the subscriber's current terminal state. Returns nil when not
// terminal.
func (s *subscriber) terminalError() error {
	if !s.active {
		switch {
		case s.removed && s.drainRemaining:
			return ErrDrained
		case s.removed:
			return ErrSubscriptionGone
		default:
			return ErrDisconnected
		}
	}
	return nil
}

// signal wakes any consumer blocked in Next. Closing (rather than sending)
// means every waiter and every polling consumer is released at once.
func (s *subscriber) signal() {
	close(s.notify)
}

// recordDrop accounts for one lost event and returns the sequence number to
// expose as LastDropSeq.
func (s *subscriber) recordDrop(seq int64) {
	s.dropped++
	s.lastDrop = seq
}

// offer attempts to place one event into the subscriber's bounded queue
// according to its overflow policy. It is called while the dispatcher lock
// is held and never blocks: a full queue is handled locally without
// affecting the producer or any other subscriber.
//
// It returns true when the consumer should be signalled (a new event became
// readable) and false when nothing was enqueued.
func (s *subscriber) offer(ev Event) (signalled bool) {
	s.offered++
	if s.queue.len() < len(s.queue.buf) {
		s.queue.push(ev)
		s.enqueued++
		return true
	}

	switch s.policy {
	case DropOldest:
		oldest := s.queue.peekOldest()
		s.recordDrop(oldest.Seq)
		_, _ = s.queue.pop()
		s.queue.push(ev)
		s.enqueued++
		return true
	case DropNewest:
		s.recordDrop(ev.Seq)
		return false
	default: // Disconnect
		s.recordDrop(ev.Seq)
		s.active = false
		// No event is enqueued, but the terminal transition must wake a
		// blocked Next so it can drain and then observe ErrDisconnected.
		return true
	}
}
