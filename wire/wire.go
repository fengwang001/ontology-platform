// Package wire implements the low-level encoding primitives of the
// ontology message format: varints, field headers, wire-type
// classification and length validation.
//
// Encoding layout of one field:
//
//	[field number varint][wire type 1 byte][length varint, only for
//	bytes/message][payload]
//
// The package depends on nothing outside the standard library.
package wire

import "errors"

// Syntax error categories. Every parse failure wraps exactly one of
// these sentinels (inside a *ParseError), so callers can distinguish
// categories with errors.Is.
var (
	// ErrVarintOverflow: a varint did not terminate within 10 bytes.
	ErrVarintOverflow = errors.New("wire: varint exceeds 10 bytes")
	// ErrVarintTruncated: the buffer ended in the middle of a varint.
	ErrVarintTruncated = errors.New("wire: buffer ends mid-varint")
	// ErrNonCanonicalVarint: a varint uses more bytes than necessary
	// (e.g. 0x80 0x00 for zero). Rejected so that re-encoding a parsed
	// message reproduces the input byte for byte.
	ErrNonCanonicalVarint = errors.New("wire: non-canonical varint encoding")
	// ErrUnknownWireType: the wire-type byte is not Varint/Bytes/Message.
	ErrUnknownWireType = errors.New("wire: unknown wire type")
	// ErrLengthOverflow: a length prefix claims more bytes than remain.
	ErrLengthOverflow = errors.New("wire: declared length exceeds remaining bytes")
	// ErrFieldNumZero: field number 0 is reserved and invalid.
	ErrFieldNumZero = errors.New("wire: field number 0 is invalid")
	// ErrFieldNumTooLarge: field number exceeds 2^32-1.
	ErrFieldNumTooLarge = errors.New("wire: field number exceeds 2^32-1")
	// ErrTruncatedHeader: the buffer ended before the header completed.
	ErrTruncatedHeader = errors.New("wire: buffer ends mid-header")
	// ErrNestedLengthMismatch: the fields inside a length-prefixed
	// nested message do not align with the declared length, i.e. an
	// inner field crosses the declared boundary.
	ErrNestedLengthMismatch = errors.New("wire: nested length does not match consumed bytes")
)

// ParseError ties a syntax error category to the absolute byte offset
// in the original input where it was detected.
type ParseError struct {
	Err    error // one of the sentinels above
	Offset int   // byte offset in the original input
}

// Error implements the error interface.
func (e *ParseError) Error() string {
	return e.Err.Error() + " (byte offset " + itoa(e.Offset) + ")"
}

// Unwrap exposes the error category for errors.Is/As.
func (e *ParseError) Unwrap() error { return e.Err }

// AddOffset shifts the offset of a *ParseError by base. Other errors
// pass through unchanged. A nil error stays nil.
func AddOffset(err error, base int) error {
	var pe *ParseError
	if errors.As(err, &pe) {
		return &ParseError{Err: pe.Err, Offset: pe.Offset + base}
	}
	return err
}

// itoa formats a non-negative int without importing strconv.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
