package pct

import "fmt"

// Kind classifies a percent-encoding failure.
type Kind int

const (
	// KindShortEscape means a '%' is followed by fewer than two hex digits.
	KindShortEscape Kind = iota + 1
	// KindBadHex means a '%' is followed by a non-hexadecimal character.
	KindBadHex
	// KindInvalidUTF8 means the decoded bytes are not valid UTF-8.
	KindInvalidUTF8
)

func (k Kind) String() string {
	switch k {
	case KindShortEscape:
		return "short percent escape"
	case KindBadHex:
		return "bad percent escape"
	case KindInvalidUTF8:
		return "invalid utf-8 after decoding"
	default:
		return "unknown escape error"
	}
}

// Error describes a percent-encoding failure and is mutually distinguishable
// from the other two kinds via Kind. Offset is a byte offset in the input.
type Error struct {
	Kind   Kind
	Offset int
}

func (e *Error) Error() string {
	return fmt.Sprintf("pct: %s at byte offset %d", e.Kind, e.Offset)
}

// AsError reports whether err is a *pct.Error and returns it.
func AsError(err error) (*Error, bool) {
	e, ok := err.(*Error)
	return e, ok
}

