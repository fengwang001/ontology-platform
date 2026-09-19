package ontology

import (
	"sync"
	"sync/atomic"
)

// SubscribeOptions declares a subscription's match criteria, buffer
// bound, and overflow/drain policies.
type SubscribeOptions struct {
	// ID identifies the subscription. If empty, one is generated.
	ID string
	// Prefix matches entities by strict prefix (never substring).
	Prefix string
	// Properties restricts matching to these exact property names.
	// Empty matches all properties of the entity.
	Properties []string
	// BufferSize is the bounded queue capacity. Must be positive.
	BufferSize int
	// Full decides overflow behavior. Zero value is FullDropOldest.
	Full FullPolicy
	// Drain decides what happens to queued messages when the
	// subscription ends. Zero value is DrainRead.
	Drain DrainPolicy
}

// Subscription is a handle to one subscriber's view of the stream.
type Subscription struct {
	id      string
	matcher matcher
	queue   *queue
	disp    *Dispatcher

	active    atomic.Bool
	closeOnce sync.Once
}

// ID returns the subscription identifier.
func (s *Subscription) ID() string { return s.id }

// Receive blocks until a message is available. It returns ok=false once
// the subscription has ended and its queue is drained.
func (s *Subscription) Receive() (Message, bool) {
	return s.queue.pop()
}

// TryReceive is the non-blocking variant of Receive.
func (s *Subscription) TryReceive() (Message, bool) {
	return s.queue.tryPop()
}

// Dropped returns the cumulative number of dropped messages and the
// sequence number of the most recent drop (0 if none).
func (s *Subscription) Dropped() (count uint64, lastSeq uint64) {
	return s.queue.stats()
}

// Cancel unsubscribes. No further messages are delivered; queued
// messages are handled per the drain policy. It is idempotent and safe
// to call concurrently with Publish.
func (s *Subscription) Cancel() {
	s.closeOnce.Do(s.terminate)
	s.disp.remove(s.id)
}

// disconnect marks the subscription lagging and terminates it without
// touching the dispatcher's lock (called while fanning out).
func (s *Subscription) disconnect() {
	s.closeOnce.Do(s.terminate)
}

func (s *Subscription) terminate() {
	s.active.Store(false)
	s.queue.close()
}

func (s *Subscription) isActive() bool {
	return s.active.Load()
}
