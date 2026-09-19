
package fanout

import "errors"

// ErrDispatcherClosed is returned by Publish after the dispatcher has been
// closed, and by Subscribe when called on a closed dispatcher.
var ErrDispatcherClosed = errors.New("fanout: dispatcher closed")

// ErrInvalidArgument is returned when subscription parameters are invalid.
var ErrInvalidArgument = errors.New("fanout: invalid argument")

// DropPolicy decides what happens when a subscriber's bounded queue is full.
type DropPolicy int

const (
	// DropOldest evicts the oldest queued message to make room for the new
	// one. The evicted message is counted as dropped for that subscriber.
	DropOldest DropPolicy = iota

	// DropNewest keeps the queued messages and discards the incoming message.
	// The discarded message is counted as dropped for that subscriber.
	DropNewest

	// DropDisconnect marks the subscriber as lagged ("detached") and stops
	// delivering to it. Every subsequently missed message is counted as
	// dropped so the missed sequence interval stays reconstructable.
	DropDisconnect
)

func (p DropPolicy) valid() bool {
	return p == DropOldest || p == DropNewest || p == DropDisconnect
}

// CancelPolicy decides how a subscription is wound down on unsubscribe or
// dispatcher close.
type CancelPolicy int

const (
	// CancelDrain leaves already queued messages readable; after the
	// subscriber consumes them the channel is observed as closed.
	CancelDrain CancelPolicy = iota

	// CancelPurge discards every queued message immediately and closes the
	// channel.
	CancelPurge
)

func (p CancelPolicy) valid() bool {
	return p == CancelDrain || p == CancelPurge
}
