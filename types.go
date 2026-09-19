// Package ontology implements a subscription and fan-out dispatcher for
// entity attribute changes. It only handles delivery: storage, links,
// actions and HTTP are out of scope.
package ontology

import "errors"

// Sentinel errors returned by the dispatcher.
var (
	// ErrClosed is returned by Publish and Subscribe after Close.
	ErrClosed = errors.New("ontology: dispatcher is closed")
	// ErrInvalidOptions is returned by Subscribe for bad options.
	ErrInvalidOptions = errors.New("ontology: invalid subscribe options")
)

// Message is a single attribute change delivered to subscribers.
type Message struct {
	// Seq is a global, monotonically increasing sequence number
	// assigned by the dispatcher at Publish time.
	Seq    uint64
	Entity string
	Attr   string
	Value  any
}

// Policy decides what happens when a subscriber's bounded queue is full.
type Policy int

const (
	// DropOldest evicts the oldest queued message to make room.
	DropOldest Policy = iota
	// DropNewest rejects the incoming message.
	DropNewest
	// Disconnect marks the subscriber as lagging and disconnects it.
	Disconnect
)

// SubscribeOptions declares a subscription's match rule and overflow
// behavior.
type SubscribeOptions struct {
	// Prefix matches entities by exact string prefix (no substring
	// semantics). An empty prefix matches every entity.
	Prefix string
	// Attrs is the set of attribute names to match, compared by exact
	// equality. An empty set matches every attribute.
	Attrs []string
	// Capacity is the bounded queue size; must be >= 1.
	Capacity int
	// Policy is the queue-full behavior.
	Policy Policy
	// DrainOnCancel controls what happens to messages still queued when
	// the subscription ends (Unsubscribe or Close): true lets the
	// consumer read them out before the channel closes; false discards
	// them (counted as dropped).
	DrainOnCancel bool
}

func (p Policy) valid() bool {
	return p == DropOldest || p == DropNewest || p == Disconnect
}
