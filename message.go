// Package ontology implements a subscription and fan-out dispatcher
// for entity property changes. It does not implement storage, links,
// actions, or HTTP; it only fans published changes out to matching
// subscribers, each of which owns a bounded queue.
package ontology

import "errors"

// ErrClosed is returned by Publish and Subscribe after the dispatcher
// has been closed.
var ErrClosed = errors.New("ontology: dispatcher closed")

// Message is a single property change delivered to subscribers.
// Seq is a global, monotonically increasing sequence number assigned
// by the dispatcher at Publish time. A subscriber that matches every
// published message observes strictly increasing Seq values, where any
// gap corresponds exactly to dropped messages.
type Message struct {
	Seq      uint64
	Entity   string
	Property string
	Value    any
}

// FullPolicy decides what happens when a subscriber's bounded queue is
// full at delivery time. The policy is per subscription and never
// affects other subscriptions.
type FullPolicy int

const (
	// DropOldest evicts the oldest queued message to make room for the
	// incoming one. The evicted message is counted as dropped.
	DropOldest FullPolicy = iota
	// DropNewest discards the incoming message. The incoming message is
	// counted as dropped.
	DropNewest
	// Disconnect marks the subscriber as lagging and tears the
	// subscription down. The incoming message and (unless DrainOnClose
	// is set) any still-queued messages are counted as dropped.
	Disconnect
)

// Options declares a subscription's match criteria and queue behavior.
type Options struct {
	// EntityPrefix matches entities by exact byte-prefix (never by
	// substring): entity "superuser" does not match prefix "user".
	// An empty prefix matches every entity.
	EntityPrefix string
	// Properties restricts delivery to these property names, matched by
	// exact equality. An empty set matches every property.
	Properties []string
	// Buffer is the bounded queue capacity. Values below 1 are
	// treated as 1.
	Buffer int
	// OnFull selects the policy applied when the queue is full.
	OnFull FullPolicy
	// DrainOnClose controls what happens to messages still queued when
	// the subscription ends (Unsubscribe, Disconnect, or dispatcher
	// Close): if true they remain readable until the channel is
	// exhausted; if false they are discarded and counted as dropped.
	DrainOnClose bool
}
