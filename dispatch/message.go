// Package dispatch implements a property-change subscription and fan-out
// dispatcher. Producers publish attribute changes for entities; the
// dispatcher fans each change out to every matching subscriber through a
// bounded per-subscriber queue, so a slow subscriber never blocks the
// producer or other subscribers.
package dispatch

import "errors"

// ErrClosed is returned by Publish and Subscribe after the dispatcher has
// been closed.
var ErrClosed = errors.New("dispatch: dispatcher is closed")

// ErrInvalidCapacity is returned by Subscribe when the queue capacity is
// less than one.
var ErrInvalidCapacity = errors.New("dispatch: queue capacity must be >= 1")

// Message is a single property-change event. Seq is a globally monotonically
// increasing sequence number assigned by the dispatcher at Publish time.
// A subscriber always observes strictly increasing Seq values; gaps in the
// sequence correspond exactly to messages that were dropped for that
// subscriber.
type Message struct {
	Seq    uint64
	Entity string
	Attr   string
	Value  any
}

// FullPolicy decides what happens when a subscriber's bounded queue is full
// at fan-out time.
type FullPolicy int

const (
	// DropOldest evicts the oldest queued message to make room for the new
	// one. The evicted message is counted as dropped.
	DropOldest FullPolicy = iota
	// DropNewest discards the incoming message when the queue is full. The
	// incoming message is counted as dropped.
	DropNewest
	// Disconnect marks the subscriber as lagging and disconnects it: the
	// incoming message is dropped, pending messages are discarded, and the
	// receive channel is closed. All discarded messages are counted.
	Disconnect
)

// PendingPolicy decides what happens to messages still queued (but not yet
// received) when a subscription is cancelled or the dispatcher is closed.
type PendingPolicy int

const (
	// Drain lets the receiver read out the remaining queued messages before
	// the channel closes.
	Drain PendingPolicy = iota
	// DiscardPending drops the remaining queued messages immediately; they
	// are counted as dropped.
	DiscardPending
)

// Options declares a subscription's match criteria and queue behaviour.
type Options struct {
	// EntityPrefix matches entities by path segment: an entity matches if
	// it equals the prefix or starts with prefix+"/". An empty prefix
	// matches every entity. Prefix "user" never matches "superuser".
	EntityPrefix string
	// Attrs is the set of attribute names to match, compared by exact
	// equality. An empty set matches every attribute.
	Attrs []string
	// Capacity is the bounded queue size; must be >= 1.
	Capacity int
	// OnFull selects the full-queue policy.
	OnFull FullPolicy
	// OnCancel selects how pending messages are treated on Cancel/Close.
	OnCancel PendingPolicy
}
