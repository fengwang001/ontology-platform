package message

import "errors"

// Limits configures the resource bounds enforced during parsing. A zero
// value for any field means "no limit" for that dimension.
type Limits struct {
	// MaxMessageBytes bounds the total size of the top-level message.
	MaxMessageBytes int
	// MaxPayloadBytes bounds the payload size of any single field,
	// including nested message payloads.
	MaxPayloadBytes int
	// MaxUnknownFields bounds the total number of unknown fields across
	// the whole message, nested messages included.
	MaxUnknownFields int
	// MaxDepth bounds the nesting depth of message-typed fields. The
	// top-level message is depth 1.
	MaxDepth int
}

// DefaultLimits returns conservative defaults suitable for most services.
func DefaultLimits() Limits {
	return Limits{
		MaxMessageBytes:  1 << 20,
		MaxPayloadBytes:  1 << 20,
		MaxUnknownFields: 1024,
		MaxDepth:         32,
	}
}

var (
	// ErrMessageTooLarge: the input exceeds Limits.MaxMessageBytes.
	ErrMessageTooLarge = errors.New("message: input exceeds maximum message size")
	// ErrPayloadTooLarge: a field payload exceeds Limits.MaxPayloadBytes.
	ErrPayloadTooLarge = errors.New("message: field payload exceeds maximum size")
	// ErrTooManyUnknowns: unknown field count exceeds Limits.MaxUnknownFields.
	ErrTooManyUnknowns = errors.New("message: too many unknown fields")
	// ErrDepthExceeded: nesting exceeds Limits.MaxDepth.
	ErrDepthExceeded = errors.New("message: nesting depth exceeds maximum")
)
