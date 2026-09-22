// Package message parses and re-serialises the custom wire format
// while preserving every byte the parser does not understand. It
// depends on packages wire and unknown; neither dependency points
// back here.
package message

import "errors"

// Limits are checked while parsing, before bytes are buffered:
// exceeding any of them rejects the whole message and leaves no
// partial state behind.
type Limits struct {
	// MaxMessageBytes bounds the total input size. Zero means use
	// DefaultMaxMessageBytes; a negative value disables the check.
	MaxMessageBytes int
	// MaxFieldPayloadBytes bounds one length-prefixed payload. Zero
	// means use DefaultMaxFieldPayloadBytes; negative disables it.
	MaxFieldPayloadBytes int
	// MaxUnknown bounds retained unknown fields per message (and per
	// nested message). Zero means use DefaultMaxUnknown; negative
	// disables it.
	MaxUnknown int
	// MaxNesting bounds nested message depth: the top level is depth
	// 1, a nested field depth 2, and so on. Zero means use
	// DefaultMaxNesting.
	MaxNesting int
}

// Default limits.
const (
	DefaultMaxMessageBytes      = 64 * 1024 * 1024
	DefaultMaxFieldPayloadBytes = 16 * 1024 * 1024
	DefaultMaxUnknown           = 1024
	DefaultMaxNesting           = 32
)

func (l Limits) messageBytes() int {
	if l.MaxMessageBytes == 0 {
		return DefaultMaxMessageBytes
	}
	return l.MaxMessageBytes
}

func (l Limits) fieldPayloadBytes() int {
	if l.MaxFieldPayloadBytes == 0 {
		return DefaultMaxFieldPayloadBytes
	}
	return l.MaxFieldPayloadBytes
}

func (l Limits) unknown() int {
	if l.MaxUnknown == 0 {
		return DefaultMaxUnknown
	}
	return l.MaxUnknown
}

func (l Limits) nesting() int {
	if l.MaxNesting == 0 {
		return DefaultMaxNesting
	}
	return l.MaxNesting
}

// ErrTooDeep is returned when nesting exceeds the configured depth.
var ErrTooDeep = errors.New("message: nesting depth exceeds limit")

// ErrTypeMismatch is returned when a known field number appears with
// a wire type other than the declared one. Such an occurrence is a
// malformed message rather than a silently retained unknown field.
var ErrTypeMismatch = errors.New("message: field wire type does not match schema")
