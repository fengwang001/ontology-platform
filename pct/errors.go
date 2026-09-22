// Package pct performs percent-encoding normalization on raw component bytes.
//
// It decodes redundant escapes of unreserved characters only, upper-cases the
// hex digits of the remaining escapes, and reports malformed escapes or invalid
// UTF-8 with exact byte offsets. It never uses the Unicode replacement
// character and never emits a partially decoded result.
package pct

import (
	"errors"
	"fmt"
)

// Error kinds. All errors are wrapped by EscapeError so callers can inspect
// Kind and Offset; sentinels support errors.Is.
var (
	// ErrShortEscape means a '%' is not followed by two hexadecimal digits.
	ErrShortEscape = errors.New("pct: truncated percent escape")
	// ErrBadHex means the two characters after '%' are not hexadecimal.
	ErrBadHex = errors.New("pct: non-hexadecimal percent escape")
	// ErrInvalidUTF8 means decoding produced bytes that are not valid UTF-8.
	ErrInvalidUTF8 = errors.New("pct: decoded bytes are not valid UTF-8")
)

// Kind identifies a malformed-escape category.
type Kind int

const (
	KindShort Kind = iota + 1
	KindBadHex
	KindUTF8
)

// EscapeError describes a malformed escape with its byte offset.
type EscapeError struct {
	Kind   Kind
	Offset int
	wrap   error
}

func (e *EscapeError) Error() string {
	return fmt.Sprintf("%v at byte offset %d", e.wrap, e.Offset)
}

func (e *EscapeError) Unwrap() error { return e.wrap }

var hexTable = [256]byte{}

func init() {
	const hexdigits = "0123456789ABCDEF"
	for i := range hexTable {
		hexTable[i] = 0xFF
	}
	for i := 0; i < 16; i++ {
		hexTable[hexdigits[i]] = byte(i)
		hexTable[hexdigits[i]+('a'-'A')] = byte(i)
	}
}

// scans counts byte visits accumulated across all Normalize calls. Each input
// byte is visited a constant number of times, so the total is linear in input
// length rather than quadratic. It is unexported and exposed read-only through
// ScanCount/ResetScans for the linear-complexity test.
var scans int64

// ScanCount returns the accumulated number of byte scans.
func ScanCount() int64 { return scans }

// ResetScans resets the byte-scan counter to zero.
func ResetScans() { scans = 0 }
