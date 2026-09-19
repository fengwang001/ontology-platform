package ontology

import "errors"

// OverflowPolicy decides what happens when a subscriber's bounded queue is
// full at the moment a new event must be delivered.
type OverflowPolicy int

const (
	// DropOldest removes the oldest queued event to make room for the new one.
	DropOldest OverflowPolicy = iota
	// DropNewest keeps the queue untouched and discards the new event.
	DropNewest
	// LagDisconnect marks the subscriber as lagging and disconnects it; no
	// further events are delivered after the overflowing event.
	LagDisconnect
)

// TailPolicy decides what happens to events already buffered in a
// subscriber's queue when the subscription is cancelled or the dispatcher
// is closed.
type TailPolicy int

const (
	// DrainPending lets the subscriber read out everything already queued.
	DrainPending TailPolicy = iota
	// DropPending discards everything still queued and closes immediately.
	DropPending
)

// Change is one published entity attribute change.
type Change struct {
	Entity    string
	Attribute string
	NewValue  string
}

// Event is a Change enriched with its global, monotonically increasing
// sequence number assigned by the dispatcher.
type Event struct {
	Seq    uint64
	Change Change
}

// Stats reports what a subscription has dropped.
type Stats struct {
	// Dropped is the cumulative number of events discarded for this
	// subscription (overflow and, when configured, tail drops).
	Dropped uint64
	// LastDropSeq is the sequence number of the most recently dropped event.
	LastDropSeq uint64
	// Lagging is true once a LagDisconnect subscription overflowed.
	Lagging bool
	// Closed is true once the subscription's channel has been closed.
	Closed bool
}

var (
	// ErrClosed is returned by Publish and Subscribe after the dispatcher
	// has been closed.
	ErrClosed = errors.New("ontology: dispatcher closed")
	// ErrInvalidBuffer is returned when a non-positive buffer is requested.
	ErrInvalidBuffer = errors.New("ontology: buffer must be positive")
)
