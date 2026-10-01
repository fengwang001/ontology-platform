// Package lzw implements a GIF-style variable-width-code LZW codec.
package lzw

import "strconv"

// Sentinel errors. Use errors.Is to distinguish failure reasons.
var (
	ErrFirstCodeNotClear = newSentinel("lzw: first code is not clear code")
	ErrFirstAfterClear   = newSentinel("lzw: first code after clear is not a literal")
	ErrInvalidCode       = newSentinel("lzw: code is greater than the next entry to be added")
	ErrPaddingNonZero    = newSentinel("lzw: padding bits after end-of-information code are non-zero")
	ErrTrailingData      = newSentinel("lzw: extra data present after end-of-information code")
	ErrTruncated         = newSentinel("lzw: input truncated before end-of-information code")
	ErrWriteAfterClose   = newSentinel("lzw: Write called after Close")
)

type lzwError string

func newSentinel(msg string) error { return lzwError(msg) }

func (e lzwError) Error() string { return string(e) }

// CodeError wraps a decode failure with the 1-based ordinal of the offending
// code in the stream (the leading clear code is code 1).
type CodeError struct {
	CodeIndex int
	Err       error
}

func (e *CodeError) Error() string {
	return e.Err.Error() + " at code index " + strconv.Itoa(e.CodeIndex)
}

func (e *CodeError) Unwrap() error { return e.Err }
