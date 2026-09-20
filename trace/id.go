// Package trace implements derivation and propagation of tracing
// contexts: spans form a tree sharing a TraceID, carry a sampling
// decision inherited from the parent, and propagate baggage
// key-value pairs with copy-on-write semantics.
package trace

import "errors"

// SpanID identifies a span (or a trace, at the root) on the wire.
type SpanID string

// ErrMalformed is returned by Parse when the wire format is invalid.
var ErrMalformed = errors.New("trace: malformed wire format")
