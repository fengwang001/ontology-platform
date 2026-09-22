// Package wire implements the low-level encoding of the custom message
// format: varints, field headers, wire types and byte-offset error
// reporting. It depends on no other package in this module.
package wire

import "errors"

// Wire types. Only these three values are legal.
const (
	Varint  byte = 0
	Bytes   byte = 1
	Message byte = 2
)

// Field number zero is reserved and illegal on the wire.
const MinFieldNumber = 1

// Parse errors. They are sentinel values: callers can distinguish the
// syntax failures with errors.Is. Concrete failures wrap the relevant
// sentinel inside a *ParseError carrying the byte offset.
var (
	ErrVarintTooLong   = errors.New("wire: varint exceeds 10 bytes")
	ErrUnknownWireType = errors.New("wire: unknown wire type")
	ErrLengthOverflow  = errors.New("wire: length prefix exceeds remaining bytes")
	ErrFieldNumberZero = errors.New("wire: field number 0 is illegal")
	ErrNestedLength    = errors.New("wire: nested length does not match consumed bytes")

	// ErrSizeLimit and ErrFieldSizeLimit are raised while enforcing
	// configurable size limits.
	ErrSizeLimit      = errors.New("wire: message exceeds maximum size")
	ErrFieldSizeLimit = errors.New("wire: field payload exceeds maximum size")
)

// ParseError reports a parse failure and the byte offset at which it
// occurred, measured from the start of the buffer passed to the parser
// that detected the error.
type ParseError struct {
	Err    error
	Offset int
}

func (e *ParseError) Error() string { return e.Err.Error() }
func (e *ParseError) Unwrap() error { return e.Err }

// NewParseError wraps err with the given offset.
func NewParseError(err error, offset int) *ParseError {
	return &ParseError{Err: err, Offset: offset}
}
