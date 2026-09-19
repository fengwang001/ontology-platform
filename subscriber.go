package ontology

import "sync"

// Subscription is a handle to one subscriber's registration. It is safe
// for concurrent use.
type Subscription struct {
	d *Dispatcher

	id            uint64
	prefix        string
	attrs         map[string]struct{} // nil means "match all attributes"
	attrNames     []string            // sorted copy for stable reporting
	policy        Policy
	drainOnCancel bool

	ch chan Message

	mu             sync.Mutex
	closed         bool
	lagging        bool
	dropped        uint64
	lastDroppedSeq uint64
}

// ID returns the subscription's unique, monotonically assigned ID.
func (s *Subscription) ID() uint64 { return s.id }

// C returns the receive channel for this subscription. It is closed when
// the subscription ends; with DrainOnCancel the remaining queued messages can
// still be read before the close is observed.
func (s *Subscription) C() <-chan Message { return s.ch }

// Dropped returns the total number of messages dropped for this
// subscription, including queue-full drops and messages discarded on
// cancel/close without drain.
func (s *Subscription) Dropped() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dropped
}

// LastDroppedSeq returns the sequence number of the most recently dropped
// message, or 0 if nothing was dropped.
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

// Unsubscribe detaches the subscription. It is idempotent: repeated calls
// return nil and never panic. After Unsubscribe returns, no further
// messages are delivered; queued messages are handled per DrainOnCancel.
func (s *Subscription) Unsubscribe() error {
	s.d.removeSub(s)
	return nil
}

// deliver attempts a non-blocking enqueue of m. It reports whether the
// subscription disconnected itself (Disconnect policy on a full queue).
func (s *Subscription) deliver(m Message) (disconnected bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	switch s.policy {
	case DropNewest:
		select {
		case s.ch <- m:
		default:
			s.recordDrop(m.Seq)
		}
	case DropOldest:
		select {
		case s.ch <- m:
		default:
			select {
			case old := <-s.ch:
				s.recordDrop(old.Seq)
			default:
			}
			select {
			case s.ch <- m:
			default:
				// Unreachable with Capacity >= 1, but stay
				// non-blocking and accountable regardless.
				s.recordDrop(m.Seq)
			}
		}
	case Disconnect:
		select {
		case s.ch <- m:
		default:
			s.recordDrop(m.Seq)
			s.lagging = true
			s.closeLocked()
			return true
		}
	}
	return false
}

// recordDrop accounts for one dropped message. Caller must hold s.mu.
func (s *Subscription) recordDrop(seq uint64) {
	s.dropped++
	s.lastDroppedSeq = seq
}

// closeLocked terminates the subscription. Queued messages are either
// left readable (DrainOnCancel) or discarded and counted as dropped.
// Caller must hold s.mu.
func (s *Subscription) closeLocked() {
	if s.closed {
		return
	}
	s.closed = true
	if !s.drainOnCancel {
		for {
			select {
			case m := <-s.ch:
				s.recordDrop(m.Seq)
			default:
				close(s.ch)
				return
			}
		}
	}
	close(s.ch)
}
