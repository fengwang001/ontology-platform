// Package dispatch implements a property-change subscription and fan-out
// dispatcher. Producers publish property changes; the dispatcher fans each
// change out to every matching subscriber through that subscriber's own
// bounded queue, so a slow subscriber never blocks producers or peers.
package dispatch

import "errors"

// ErrClosed is returned by Publish and Subscribe after the dispatcher has
// been closed. It can be tested with errors.Is.
var ErrClosed = errors.New("dispatch: dispatcher is closed")

// DropPolicy decides what happens when a subscriber's bounded queue is full
// at the moment a new message is fanned out to it.
type DropPolicy int

const (
	// DropOldest evicts the oldest queued message to make room for the new
	// one. The evicted message is counted as dropped.
	DropOldest DropPolicy = iota
	// DropNewest discards the incoming message when the queue is full.
	DropNewest
	// Disconnect marks the subscriber as lagging and disconnects it: it is
	// removed from the dispatcher and receives no further messages.
	Disconnect
)

// String returns a human-readable name for the policy.
func (p DropPolicy) String() string {
	switch p {
	case DropOldest:
		return "drop-oldest"
	case DropNewest:
		return "drop-newest"
	case Disconnect:
		return "disconnect"
	default:
		return "unknown"
	}
}

// Message is a single property-change event delivered to subscribers.
//
// Seq is a global, monotonically increasing sequence number assigned by the
// dispatcher at Publish time. The sequence numbers a subscriber receives are
// strictly increasing; gaps correspond exactly to messages that were dropped
// for that subscriber.
type Message struct {
	Seq      uint64
	Entity   string
	Property string
	Value    any
}
