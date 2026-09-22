package hexline

// Kind classifies a chunk-size-line failure.
type Kind int

const (
	// KindNonHex means a byte appeared where a hexadecimal digit was required.
	KindNonHex Kind = iota + 1
	// KindLineTooLong means the size line exceeded the configured byte limit.
	KindLineTooLong
	// KindUnterminatedQuote means a quoted string had no closing quote.
	KindUnterminatedQuote
)

// Error describes a size-line parse failure and the byte offset (0-based,
// measured from the first byte handed to the Parser) at which it occurred.
type Error struct {
	Kind   Kind
	Offset int
}

func (e *Error) Error() string {
	switch e.Kind {
	case KindNonHex:
		return "hexline: non-hexadecimal chunk size"
	case KindLineTooLong:
		return "hexline: chunk size line too long"
	case KindUnterminatedQuote:
		return "hexline: unterminated quoted string in chunk extension"
	default:
		return "hexline: invalid chunk size line"
	}
}

// AsError reports whether err is a *hexline.Error and, if so, returns it.
func AsError(err error) (*Error, bool) {
	e, ok := err.(*Error)
	return e, ok
}
