package ontology

import "errors"

// ErrClosed is returned by Publish and Subscribe after the dispatcher has
// been closed.
var ErrClosed = errors.New("ontology: dispatcher closed")

// ErrInvalidBuffer is returned when a subscription requests a buffer of zero
// or a negative size.
var ErrInvalidBuffer = errors.New("ontology: buffer size must be positive")

// ErrSubscriptionGone is returned (through Stats) when a subscriber has been
// disconnected by the DropNewestAndDisconnect policy.
var ErrSubscriptionGone = errors.New("ontology: subscription disconnected as too far behind")
