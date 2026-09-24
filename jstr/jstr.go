package jstr

import "errors"

// Sentinel errors identify the five strict-decode failure classes and the
// invalid-UTF-8 failures.
var (
	ErrControlChar      = errors.New("jstr: unescaped control character")
	ErrUnknownEscape    = errors.New("jstr: unknown escape sequence")
	ErrBadUnicodeEscape = errors.New("jstr: invalid \\u escape")
	ErrMissingQuote     = errors.New("jstr: missing closing quote")
	ErrTrailingBytes    = errors.New("jstr: trailing bytes after closing quote")
	ErrInvalidUTF8      = errors.New("jstr: invalid UTF-8")
)

// Error reports the failure class and its byte offset within the input.
type Error struct {
	Kind   error
	Offset int
}

func (e *Error) Error() string { return e.Kind.Error() }
func (e *Error) Unwrap() error { return e.Kind }

// Decode strictly decodes one quoted JSON string literal.
func Decode(lit []byte) (string, error) { return "", &Error{Kind: ErrMissingQuote, Offset: 0} }

// Encode emits the minimal-escape JSON string literal for s.
func Encode(s string) ([]byte, error) { return nil, nil }

// Writer is a streaming strict decoder; feed the whole literal (including
// both quotes) through Write, then call Close.
type Writer struct {
	BytesChecked int
}

func (w *Writer) Write(p []byte) (int, error) { return len(p), nil }
func (w *Writer) Close() error                { return nil }
func (w *Writer) Result() string              { return "" }
