package pct

import "fmt"

// EscapeError is a malformed percent-escape error located at a byte offset.
type EscapeError struct {
	// Kind is "truncated", "badhex" or "utf8".
	Kind string
	// Offset is the zero-based byte offset in the input ("%" for truncated/
	// badhex; first byte of the invalid run for utf8).
	Offset int
}

func (e *EscapeError) Error() string {
	switch e.Kind {
	case KindTruncated:
		return fmt.Sprintf("pct: truncated percent-escape at byte %d", e.Offset)
	case KindBadHex:
		return fmt.Sprintf("pct: non-hexadecimal percent-escape at byte %d", e.Offset)
	default:
		return fmt.Sprintf("pct: invalid UTF-8 sequence at byte %d", e.Offset)
	}
}

const (
	// KindTruncated marks a "%" followed by fewer than two hex digits.
	KindTruncated = "truncated"
	// KindBadHex marks a "%" whose following digits are not hexadecimal.
	KindBadHex = "badhex"
	// KindUTF8 marks a decoded sequence that is not valid UTF-8.
	KindUTF8 = "utf8"
)

// IsTruncated reports whether err is a truncated-escape error.
func IsTruncated(err error) bool {
	e, ok := err.(*EscapeError)
	return ok && e.Kind == KindTruncated
}

// IsBadHex reports whether err is a non-hexadecimal-escape error.
func IsBadHex(err error) bool {
	e, ok := err.(*EscapeError)
	return ok && e.Kind == KindBadHex
}

// IsInvalidUTF8 reports whether err is an invalid-UTF-8 error.
func IsInvalidUTF8(err error) bool {
	e, ok := err.(*EscapeError)
	return ok && e.Kind == KindUTF8
}
