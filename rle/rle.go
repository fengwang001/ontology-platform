// Package rle implements canonical run-length encoding with escaping.
//
// A run is an optional decimal count plus one symbol (a code point).
// ASCII digits and backslash are written as '\' + char; a count of 1 is
// omitted. Decode is strict: it accepts only canonical encodings, so
// Encode(Decode(t)) == t for every accepted t. See DESIGN.md.
package rle

import (
	"errors"
	"strings"
	"unicode/utf8"

	"ontology/runs"
)

// Distinguishable decoding errors, each wrapped in *Error with an offset.
var (
	ErrExplicitOne       = errors.New("rle: explicit count 1")
	ErrZeroCount         = errors.New("rle: zero count")
	ErrLeadingZero       = errors.New("rle: count with leading zero")
	ErrAdjacentSame      = errors.New("rle: adjacent runs with equal symbols")
	ErrBadEscape         = errors.New("rle: backslash not before digit or backslash")
	ErrTrailingBackslash = errors.New("rle: trailing backslash")
	ErrMissingSymbol     = errors.New("rle: count without symbol at end")
	ErrInvalidUTF8       = errors.New("rle: invalid UTF-8")
	ErrTooLarge          = errors.New("rle: decoded output exceeds limit")
	ErrClosed            = errors.New("rle: write after close")
)

// Error is a decoding failure: Err is one of the sentinels above and
// Off is the byte offset in the encoded input where it was detected.
type Error struct {
	Err error
	Off int64
}

func (e *Error) Error() string { return e.Err.Error() + " at byte " + itoa(e.Off) }
func (e *Error) Unwrap() error { return e.Err }

func itoa(n int64) string { return string(runs.AppendCount(nil, uint64(n))) }

// DefaultMaxOutput is the default decoded-output byte limit.
const DefaultMaxOutput = 1 << 30

var inspected int64

// Inspected reports the total number of input bytes examined so far.
func Inspected() int64 { return inspected }

// ResetInspected resets the Inspected counter.
func ResetInspected() { inspected = 0 }

// Encode returns the canonical encoding of s: maximal runs, count
// omitted when 1, digits and backslash escaped.
func Encode(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range runs.Split(s) {
		if r.Count > 1 {
			b.Write(runs.AppendCount(nil, r.Count))
		}
		if runs.NeedsEscape(r.Rune) {
			b.WriteByte('\\')
		}
		b.WriteRune(r.Rune)
	}
	return b.String()
}

// Decode strictly decodes t in one shot.
func Decode(t string) (string, error) {
	d := NewDecoder()
	if _, err := d.Write([]byte(t)); err != nil {
		return "", err
	}
	return d.Close()
}

// Decoder is a streaming strict decoder. Encoded bytes may be fed via
// Write in any chunking; the result and errors are chunking-independent.
type Decoder struct {
	MaxOutput int64 // decoded-output byte limit

	state, runeN         int
	digits               []byte
	count                = // placeholder
}
