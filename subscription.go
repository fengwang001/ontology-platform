package ontology

import (
	"io"
	"sync/atomic"
)

// Subscription is a subscriber handle. One goroutine typically calls Receive
// while other goroutines may read Stats concurrently.
type Subscription struct {
	id       uint64
	box      *mailbox
	dropped  atomic.Int64 // cumulative dropped message count
	lastDrop atomic.Int64 // sequence of the most recently dropped message
	ended    atomic.Bool
}

// Receive blocks until the next matching message is available. After
// unsubscribe/close it returns io.EOF once queued messages have been handled
// according to the subscription's CancelPolicy.
func (s *Subscription) Receive() (Delivery, error) {
	d, ok := s.box.Get()
	if !ok {
		return Delivery{}, io.EOF
	}
	return d, nil
}

// DroppedTotal reports how many messages have been dropped for this
// subscriber since it was created.
func (s *Subscription) DroppedTotal() int64 { return s.dropped.Load() }

// LastDroppedSeq reports the sequence of the most recently dropped message,
// or 0 if nothing has ever been dropped.
func (s *Subscription) LastDroppedSeq() int64 { return s.lastDrop.Load() }

// Active reports whether the subscriber is still receiving new fan-out. A
// subscriber disconnected by DropNewestAndDisconnect reports false even
// while queued messages may still be drained.
func (s *Subscription) Active() bool { return !s.ended.Load() }

func (s *Subscription) recordDrop(seq int64) {
	s.dropped.Add(1)
	s.lastDrop.Store(seq)
}

func (s *Subscription) terminate(policy CancelPolicy) {
	if s.ended.Swap(true) {
		return
	}
	s.box.Terminate(policy)
}

// Stats is a point-in-time snapshot of a subscriber's accounting.
type Stats struct {
	ID           uint64
	Active       bool
	DroppedTotal int64
	LastDropped  int64
}

func (s *Subscription) stats() Stats {
	return Stats{
		ID:           s.id,
		Active:       s.Active(),
		DroppedTotal: s.DroppedTotal(),
		LastDropped:  s.LastDroppedSeq(),
	}
}
