package frame

import "ontology/internal/hexline"

// Kind classifies a single-chunk framing failure.
type Kind int

const (
	// KindNonHex: the size line contained an invalid byte.
	KindNonHex Kind = iota + 1
	// KindLineTooLong: the size line exceeded its byte limit.
	KindLineTooLong
	// KindUnterminatedQuote: a quoted extension value was never closed.
	KindUnterminatedQuote
	// KindMissingCRLF: the chunk data was followed by something other than CRLF.
	KindMissingCRLF
)

// Error describes a framing failure and the byte offset (0-based, measured
// from the first byte handed to this frame's Reader) of the offending byte.
type Error struct {
	Kind   Kind
	Offset int
	// CRSeen is true for a MissingCRLF where the leading CR arrived but the
	// stream ended before LF.
	CRSeen bool
}

func (e *Error) Error() string {
	switch e.Kind {
	case KindNonHex:
		return "frame: invalid byte in chunk size line"
	case KindLineTooLong:
		return "frame: chunk size line too long"
	case KindUnterminatedQuote:
		return "frame: unterminated quoted chunk extension"
	case KindMissingCRLF:
		return "frame: expected CRLF after chunk data"
	default:
		return "frame: framing error"
	}
}

// AsError reports whether err is a *frame.Error and returns it.
func AsError(err error) (*Error, bool) {
	e, ok := err.(*Error)
	return e, ok
}

func fromHex(err error) *Error {
	he, ok := hexline.AsError(err)
	if !ok {
		return &Error{Kind: KindNonHex}
	}
	fe := &Error{Offset: he.Offset}
	switch he.Kind {
	case hexline.KindLineTooLong:
		fe.Kind = KindLineTooLong
	case hexline.KindUnterminatedQuote:
		fe.Kind = KindUnterminatedQuote
	default:
		fe.Kind = KindNonHex
	}
	return fe
}
