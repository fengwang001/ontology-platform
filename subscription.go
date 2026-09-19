package ontology

import (
	"strings"
	"sync/atomic"
)

// Subscription is a single subscriber's view of the dispatcher. It is
// safe for concurrent use.
type Subscription struct {
	id     uint64
	prefix string
	props  map[string]struct{}
	opts   Options
	ch     chan Message
	d      *Dispatcher

	done        atomic.Bool
	lagged      atomic.Bool
	dropped     atomic.Uint64
	lastDropSeq atomic.Uint64
}

// ID returns the subscription's dispatcher-assigned identifier. IDs
// increase monotonically in subscription order.
func (s *Subscription) ID() uint64 { return s.id }

// C returns the receive channel for delivered messages. The channel is
// closed when the subscription ends; if DrainOnClose was set, buffered
// messages remain readable after closure.
func (s *Subscription) C() <-chan Message { return s.ch }

// Dropped reports how many messages this subscription has lost so far:
// evicted by the full-queue policy or discarded at teardown.
func (s *Subscription) Dropped() uint64 { return s.dropped.Load() }

// LastDropSeq reports the highest sequence number among the messages
// this subscription has dropped so far, or 0 if none.
func (s *Subscription) LastDropSeq() uint64 { return s.lastDropSeq.Load() }

// Lagged reports whether the subscription was torn down because its
// queue overflowed under the Disconnect policy.
func (s *Subscription) Lagged() bool { return s.lagged.Load() }

// Unsubscribe detaches the subscription. It is idempotent: repeated
// calls are harmless no-ops. After Unsubscribe returns, no further
// messages are delivered; queued messages are handled per DrainOnClose.
func (s *Subscription) Unsubscribe() {
	s.d.mu.Lock()
	defer s.d.mu.Unlock()
	s.teardownLocked()
}

// matches reports whether a change to entity/property should be
// delivered to this subscription. Prefix matching is an exact
// byte-prefix test and never degrades to substring matching; property
// matching is exact equality, with an empty set matching everything.
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

// deliver fans one message out to this subscription without ever
// blocking. It must be called with the dispatcher mutex held, which
// serializes it against Unsubscribe and Close.
func (s *Subscription) deliver(m Message) {
	select {
	case s.ch <- m:
		return
	default:
	}
	switch s.opts.OnFull {
	case DropNewest:
		s.noteDrop(m.Seq)
	case Disconnect:
		s.noteDrop(m.Seq)
		s.lagged.Store(true)
		s.teardownLocked()
	default: // DropOldest
		select {
		case old := <-s.ch:
			s.noteDrop(old.Seq)
		default:
			// A concurrent receiver drained the queue; fall through.
		}
		select {
		case s.ch <- m:
		default:
			s.noteDrop(m.Seq)
		}
	}
}

// teardownLocked removes the subscription and closes its channel,
// honoring DrainOnClose for still-queued messages. Idempotent.
// Must be called with the dispatcher mutex held.
func (s *Subscription) teardownLocked() {
	if s.done.Swap(true) {
		return
	}
	delete(s.d.subs, s.id)
	if s.opts.DrainOnClose {
		close(s.ch)
		return
	}
	for {
		select {
		case m := <-s.ch:
			s.noteDrop(m.Seq)
		default:
			close(s.ch)
			return
		}
	}
}

// noteDrop records one dropped message for accounting. All callers hold
// the dispatcher mutex, so the load-compare-store cannot race a writer.
func (s *Subscription) noteDrop(seq uint64) {
	s.dropped.Add(1)
	if seq > s.lastDropSeq.Load() {
		s.lastDropSeq.Store(seq)
	}
}
