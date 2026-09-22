// Package wire implements the low-level encoding primitives of the
// ontology message format: varints, field headers and payload framing.
//
// A message is a sequence of fields. Each field is encoded as:
//
//	[field number varint][wire type 1 byte][length varint, bytes/message only][payload]
//
// This package depends on nothing outside the standard library.
package wire

import "errors"

// Type is the wire type of a field, encoded as a single byte.
type Type byte

const (
	// Varint fields carry a single varint payload.
	Varint Type = 0
	// Bytes fields carry a length-prefixed raw byte payload.
	Bytes Type = 1
	// Message fields carry a length-prefixed nested message.
	Message Type = 2
)

// Valid reports whether t is one of the three known wire types.
func (t Type) Valid() bool { return t == Varint || t == Bytes || t == Message }

// Syntax errors. Every parse failure wraps exactly one of these sentinels
// inside an *Error, so callers can distinguish causes with errors.Is.
var (
	// ErrVarintOverflow: a varint did not terminate within 10 bytes.
	ErrVarintOverflow = errors.New("wire: varint exceeds 10 bytes")
	// ErrTruncated: the input ended in the middle of a field.
	ErrTruncated = errors.New("wire: unexpected end of input")
	// ErrUnknownType: the wire type byte is not varint/bytes/message.
	ErrUnknownType = errors.New("wire: unknown wire type")
	// ErrLengthOverflow: a length prefix exceeds the remaining input.
	ErrLengthOverflow = errors.New("wire: length prefix exceeds remaining input")
	// ErrZeroFieldNumber: field number 0 is not allowed.
	ErrZeroFieldNumber = errors.New("wire: field number is zero")
	// ErrLengthMismatch: a nested message's declared length does not
	// match the number of bytes its content actually consumes.
	ErrLengthMismatch = errors.New("wire: nested length does not match consumed bytes")
)

// Error is a decode failure annotated with the byte offset at which it
// was detected. Offsets are relative to the start of the top-level
// message buffer passed to the parser.
type Error struct {
	Offset int
	Err    error
}

func (e *Error) Error() string {
	return "offset " + itoa(e.Offset) + ": " + e.Err.Error()
}

// Unwrap returns the underlying sentinel, for errors.Is/As.
func (e *Error) Unwrap() error { return e.Err }

// itoa formats a non-negative int without importing strconv so the error
// path stays allocation-light and dependency-free.
func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
