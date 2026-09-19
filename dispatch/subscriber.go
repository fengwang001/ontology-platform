package dispatch

import (
	"strings"
	"sync/atomic"
)

// Subscriber is a single subscription created by Dispatcher.Subscribe.
// Messages arrive on the channel returned by Chan. Drop accounting is
// per-subscriber and safe to read from any goroutine.
type Subscriber struct {
	id     string
	prefix string
	props  map[string]struct{}
	policy DropPolicy
	drain  bool

	queue chan Message
	done  chan struct{}
	d     *Dispatcher

	dropped     atomic.Uint64
	lastDropSeq atomic.Uint64
	lagged      atomic.Bool
}

// ID returns the subscriber's unique identifier.
func (s *Subscriber) ID() string { return s.id }

// Chan returns the receive channel for this subscriber. It is closed when
// the subscription ends (Cancel, lagging disconnect, or dispatcher Close).
// If the subscription was created with DrainOnCancel, buffered messages
// remain receivable after closure; otherwise they are discarded.
func (s *Subscriber) Chan() <-chan Message { return s.queue }

// Done is closed when the subscription ends for any reason.
func (s *Subscriber) Done() <-chan struct{} { return s.done }

// Dropped returns the total number of messages dropped for this subscriber,
// including queue-full drops and messages discarded on cancellation.
func (s *Subscriber) Dropped() uint64 { return s.dropped.Load() }

// LastDropSeq returns the sequence number of the most recently dropped
// message, or 0 if nothing has been dropped.
func (s *Subscriber) LastDropSeq() uint64 { return s.lastDropSeq.Load() }

// Lagged reports whether the subscriber was disconnected because its queue
// filled up under the Disconnect policy.
func (s *Subscriber) Lagged() bool { return s.lagged.Load() }

// Cancel unsubscribes the subscriber. It is idempotent: repeated calls are
// harmless. After Cancel returns, no new messages are enqueued; buffered
// messages are drained or discarded according to DrainOnCancel.
func (s *Subscriber) Cancel() {
	s.d.mu.Lock()
	s.d.removeLocked(s, !s.drain)
	s.d.mu.Unlock()
}

// matches reports whether a change to property on entity should be
// delivered to this subscriber. Prefix matching uses strings.HasPrefix and
// never degrades to substring matching; property matching is exact, and an
// empty property set matches every property.
func (s *Subscriber) matches(entity, property string) bool {
	if !strings.HasPrefix(entity, s.prefix) {
		return false
	}
	if len(s.props) == 0 {
		return true
	}
	_, ok := s.props[property]
	return ok
}

// enqueue delivers m according to the subscriber's drop policy. It never
// blocks. The caller must hold d.mu.
func (s *Subscriber) enqueue(m Message) {
	select {
	case s.queue <- m:
		return
	default:
	}
	switch s.policy {
	case DropOldest:
		select {
		case old := <-s.queue:
			s.noteDrop(old.Seq)
		default:
		}
		select {
		case s.queue <- m:
		default:
			s.noteDrop(m.Seq)
		}
	case DropNewest:
		s.noteDrop(m.Seq)
	case Disconnect:
		s.noteDrop(m.Seq)
		s.lagged.Store(true)
		s.d.removeLocked(s, false)
	}
}

// noteDrop records one dropped message by sequence number.
func (s *Subscriber) noteDrop(seq uint64) {
	s.dropped.Add(1)
	s.lastDropSeq.Store(seq)
}
