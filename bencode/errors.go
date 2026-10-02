package bencode

import (
	"errors"
	"fmt"
)

// Rejection reasons. Use errors.Is against the *Error returned by Feed or
// Err to distinguish them.
var (
	// ErrBadLeadingByte: a value does not start with 'i', 'l', 'd' or a digit.
	ErrBadLeadingByte = errors.New("bencode: illegal leading byte")
	// ErrIntLeadingZero: an integer has a leading zero (offset: the digit
	// right after the leading '0').
	ErrIntLeadingZero = errors.New("bencode: integer with leading zero")
	// ErrNegativeZero: the integer "-0" (offset: the '0' after the sign).
	ErrNegativeZero = errors.New("bencode: negative zero")
	// ErrIntOverflow: the integer does not fit int64 (offset: the digit
	// that pushes the magnitude out of range).
	ErrIntOverflow = errors.New("bencode: integer overflows int64")
	// ErrLenLeadingZero: a string length has a leading zero (offset: the
	// digit right after the leading '0').
	ErrLenLeadingZero = errors.New("bencode: string length with leading zero")
	// ErrStringTooLong: the length prefix exceeds MaxString (offset: the
	// digit that pushes the length past the limit).
	ErrStringTooLong = errors.New("bencode: string length exceeds MaxString")
	// ErrDepthExceeded: nesting is deeper than MaxDepth (offset: the
	// opening bracket of level D+1).
	ErrDepthExceeded = errors.New("bencode: nesting depth exceeds MaxDepth")
	// ErrKeyNotString: a dictionary key is not a byte string (offset: the
	// first byte of the key).
	ErrKeyNotString = errors.New("bencode: dictionary key is not a byte string")
	// ErrKeyOutOfOrder: dictionary keys are not strictly ascending
	// (offset: the first byte of the offending key's length prefix).
	ErrKeyOutOfOrder = errors.New("bencode: dictionary keys out of order")
	// ErrDuplicateKey: a dictionary key repeats (offset: the first byte of
	// the duplicate key's length prefix).
	ErrDuplicateKey = errors.New("bencode: duplicate dictionary key")
	// ErrSyntax: any other malformed byte, e.g. a non-digit inside an
	// integer or length prefix (offset: the byte itself).
	ErrSyntax = errors.New("bencode: syntax error")
	// ErrTruncated is only used by DecodeAll when the input ends in the
	// middle of a value (offset: start of the incomplete value).
	ErrTruncated = errors.New("bencode: truncated input")
	// ErrPoisoned is returned by every Feed after the decoder has failed.
	ErrPoisoned = errors.New("bencode: decoder is poisoned by an earlier error")
)

// Error describes a rejection of the input stream.
type Error struct {
	Reason error // one of the sentinels above
	Offset int64 // absolute stream offset of the first offending byte
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s at offset %d", e.Reason, e.Offset)
}

// Unwrap exposes Reason so errors.Is(err, ErrDuplicateKey) etc. work.
func (e *Error) Unwrap() error { return e.Reason }
