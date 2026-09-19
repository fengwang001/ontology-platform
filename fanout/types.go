
package fanout

import "sync"

// Change is a single attribute change offered to the dispatcher.
type Change struct {
	// Entity identifies the changed entity.
	Entity string
	// Attr is the name of the changed attribute.
	Attr string
	// Value is the new attribute value. The dispatcher never inspects it.
	Value any
}

// Envelope wraps a delivered change with dispatcher-wide metadata.
type Envelope struct {
	// Seq is the globally monotonic sequence number assigned by Publish.
	// Across one dispatcher, every successful Publish consumes exactly one
	// number; received sequence numbers are strictly increasing per
	// subscriber, possibly with gaps where messages were dropped.
	Seq uint64
	Change
}

// Stats reports per-subscriber drop accounting. All counters are safe to
// read concurrently with publishes.
type Stats struct {
	// Dropped is the total number of messages dropped for this subscriber,
	// including drops caused by a full queue and messages missed after a
	// DropDisconnect policy detached the subscriber.
	Dropped uint64
	// LastDroppedSeq is the sequence number of the most recently dropped
	// message; zero when nothing was ever dropped.
	LastDroppedSeq uint64
	// Detached reports whether a DropDisconnect subscriber has been marked
	// lagging and disconnected.
	Detached bool
}

// Subscription is one subscriber: a bounded delivery channel plus
// independently enforced policies and drop counters.
type Subscription struct {
	id     uint64
	prefix string
	attrs  map[string]struct{}
	inCh   chan Envelope
	outCh  chan Envelope
	drain  chan struct{}
	purge  chan struct{}

	// subMu protects the bounded queue mechanics: the channel is guarded
	// together with detached/canceled/closed so delivery decisions are
	// atomic per subscriber.
	subMu sync.Mutex
	detached bool
	canceled bool
	closed   bool

	drop     DropPolicy
	onCancel CancelPolicy

	droppedCount   uint64
	lastDropSeq    uint64
	shut           bool
}

// ID returns the subscriber's unique, assignment-order identifier.
func (s *Subscription) ID() uint64 { return s.id }

// C returns the receive channel for this subscriber's queue. After close or
// unsubscribe the channel is closed; with CancelDrain it is closed only once
// the queued messages have been consumed.
func (s *Subscription) C() <-chan Envelope { return s.outCh }
