package ontology

// Event is a single property-change notification fanned out by a Dispatcher.
//
// Seq is the dispatcher-global, monotonically increasing sequence number
// assigned at Publish time. Every subscriber observes strictly increasing
// sequence numbers; gaps represent dropped events.
type Event struct {
	Seq      int64
	EntityID string
	Property string
	// Value and OldValue carry the new/previous property values; the fan-out
	// layer treats them opaquely.
	Value    any
	OldValue any
}

// OverflowPolicy decides what happens when a subscriber's bounded queue is
// full at Publish time. The policy is per-subscriber and decided at
// subscription time.
type OverflowPolicy int

const (
	// DropOldest evicts the oldest queued event to make room for the new one.
	DropOldest OverflowPolicy = iota
	// DropNewest keeps the queued events and discards the incoming event.
	DropNewest
	// Disconnect marks the subscriber as lagging and detaches it from further
	// fan-out. Events still buffered may be consumed; Next returns
	// ErrDisconnected after the buffer is exhausted.
	Disconnect
)

// Sentinel errors; errors.Is works against them directly.
var (
	// ErrClosed is returned by Publish after the dispatcher has been closed.
	ErrClosed = errClosed{}
	// ErrDisconnected is returned by Next when the subscriber overflowed with
	// the Disconnect policy and its buffer has been exhausted.
	ErrDisconnected = errDisconnected{}
	// ErrDrained is returned by Next after an unsubscribe with
	// DrainOnUnsubscribe=true once all buffered events have been consumed.
	ErrDrained = errDrained{}
	// ErrSubscriptionGone is returned by Next after an unsubscribe without
	// draining (the buffer is discarded immediately).
	ErrSubscriptionGone = errGone{}
)

type errClosed struct{}

func (errClosed) Error() string { return "ontology: dispatcher is closed" }

type errDisconnected struct{}

func (errDisconnected) Error() string {
	return "ontology: subscriber disconnected due to lag"
}

type errDrained struct{}

func (errDrained) Error() string {
	return "ontology: subscription drained after unsubscribe"
}

type errGone struct{}

func (errGone) Error() string { return "ontology: subscription removed" }
