// Package wire implements the low-level binary encoding primitives of
// the message format: varints, field headers, wire-type validation
// and length-prefix checks. It depends on nothing outside the
// standard library.
//
// A message is a sequence of fields. Each field is encoded as:
//
//	[field number varint][wire type 1 byte][length varint, only for
//	bytes/message][payload]
//
// Varints use the standard 7-bits-per-group, high-bit-continuation,
// little-endian encoding. Only canonical (shortest-form) varints are
// accepted; this is what allows a parsed message to be re-encoded
// byte-for-byte identically.
package wire

import (
	"errors"
	"fmt"
)

// Type is the wire type of a field, stored in one byte.
type Type byte

const (
	// Varint fields carry a single varint payload.
	Varint Type = 0
	// Bytes fields carry a length-prefixed raw byte payload.
	Bytes Type = 1
	// Message fields carry a length-prefixed nested message.
	Message Type = 2
)

// Valid reports whether t is one of the three defined wire types.
func (t Type) Valid() bool { return t <= Message }

// HasLength reports whether fields of this type carry a length prefix.
func (t Type) HasLength() bool { return t == Bytes || t == Message }

func (t Type) String() string {
	switch t {
	case Varint:
		return "varint"
	case Bytes:
		return "bytes"
	case Message:
		return "message"
	}
	return fmt.Sprintf("unknown(%d)", byte(t))
}

// Kind classifies syntax errors so callers can distinguish them.
type Kind int

const (
	// KindVarintOverflow: a varint did not terminate within 10 bytes.
	KindVarintOverflow Kind = iota + 1
	// KindUnknownWireType: the wire-type byte is not 0, 1 or 2.
	KindUnknownWireType
	// KindLengthOverflow: a length prefix exceeds the remaining bytes.
	KindLengthOverflow
	// KindFieldNumberZero: field number 0 is not allowed.
	KindFieldNumberZero
	// KindNestedLengthMismatch: a nested message's declared length does
	// not match the bytes its inner fields actually consume.
	KindNestedLengthMismatch
	// KindTruncated: the buffer ends in the middle of an encoding.
	KindTruncated
	// KindNonCanonicalVarint: a varint is not in shortest form.
	KindNonCanonicalVarint
)

func (k Kind) String() string {
	switch k {
	case KindVarintOverflow:
		return "varint exceeds 10 bytes"
	case KindUnknownWireType:
		return "unknown wire type"
	case KindLengthOverflow:
		return "length prefix exceeds remaining bytes"
	case KindFieldNumberZero:
		return "field number 0 is invalid"
	case KindNestedLengthMismatch:
		return "nested length does not match consumed bytes"
	case KindTruncated:
		return "buffer ends mid-encoding"
	case KindNonCanonicalVarint:
		return "non-canonical varint"
	}
	return fmt.Sprintf("kind(%d)", int(k))
}

// Error is a syntax error at a specific byte offset of the message.
type Error struct {
	Kind   Kind
	Offset int // absolute byte offset in the top-level message
}

func (e *Error) Error() string {
	return fmt.Sprintf("wire: %s at byte offset %d", e.Kind, e.Offset)
}

// IsKind reports whether err is a *wire.Error of the given kind.
func IsKind(err error, k Kind) bool {
	var we *Error
	return errors.As(err, &we) && we.Kind == k
}
