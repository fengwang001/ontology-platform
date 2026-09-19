package ontology

import "errors"

// ErrClosed is returned by Publish and Subscribe after the dispatcher
// has been closed.
var ErrClosed = errors.New("ontology: dispatcher closed")

// ErrInvalidBuffer is returned by Subscribe when BufferSize is not positive.
var ErrInvalidBuffer = errors.New("ontology: buffer size must be positive")

// Message is a single property-change event. Seq is a globally monotonic
// sequence number assigned by the dispatcher at Publish time. A subscriber
// always observes strictly increasing Seq values; gaps correspond to
// dropped messages.
type Message struct {
	Seq      uint64
	Entity   string
	Property string
	Value    any
}
