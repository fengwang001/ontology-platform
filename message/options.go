// Package message parses and serializes known message structures on
// top of the wire and unknown packages. Its central guarantee is
// byte-exact round-tripping: parsing a legal message and serializing
// it again yields the identical byte sequence, including fields the
// parser does not know.
package message

import (
	"errors"
	"strconv"
)

// Options configures the safety limits applied while parsing.
// A zero or negative value disables the corresponding limit.
type Options struct {
	// MaxMessageBytes bounds the total size of one message.
	MaxMessageBytes int
	// MaxFieldBytes bounds the payload size of a single field.
	MaxFieldBytes int
	// MaxUnknownFields bounds the number of unknown fields per parse.
	MaxUnknownFields int
	// MaxDepth bounds the nesting depth of message-typed fields.
	MaxDepth int
}

// DefaultOptions returns conservative defaults.
func DefaultOptions() Options {
	return Options{
		MaxMessageBytes:  1 << 20,
		MaxFieldBytes:    1 << 20,
		MaxUnknownFields: 1 << 14,
		MaxDepth:         64,
	}
}

// Limit violations. Returned errors wrap these sentinels in a
// *LimitError carrying the offset, so errors.Is works.
var (
	ErrMessageTooLarge = errors.New("message: total size exceeds MaxMessageBytes")
	ErrFieldTooLarge   = errors.New("message: field payload exceeds MaxFieldBytes")
	ErrTooManyUnknown  = errors.New("message: unknown field count exceeds MaxUnknownFields")
	ErrDepthExceeded   = errors.New("message: nesting depth exceeds MaxDepth")
)

// LimitError reports a rejected limit at a byte offset.
type LimitError struct {
	Err    error
	Offset int
}

func (e *LimitError) Error() string {
	return e.Err.Error() + " at byte offset " + strconv.Itoa(e.Offset)
}

func (e *LimitError) Unwrap() error { return e.Err }
