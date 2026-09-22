// Package pct implements percent-encoding decoding and normalization.
package pct

import "fmt"

// Kind classifies percent-encoding errors so callers can tell them apart.
type Kind int

const (
	// KindTruncated: a '%' is followed by fewer than two hex digits.
	KindTruncated Kind = iota
	// KindBadHex: a '%' is followed by a non-hex character.
	KindBadHex
	// KindBadUTF8: the decoded byte stream is not valid UTF-8.
	KindBadUTF8
)

func (k Kind) String() string {
	switch k {
	case KindTruncated:
		return "truncated escape"
	case KindBadHex:
		return "non-hex escape"
	case KindBadUTF8:
		return "invalid utf-8"
	}
	return "unknown"
}

// Error describes a percent-encoding failure at a byte offset of the input.
type Error struct {
	Kind   Kind
	Offset int // byte offset in the original input string
}

func (e *Error) Error() string {
	return fmt.Sprintf("pct: %s at byte offset %d", e.Kind, e.Offset)
}
