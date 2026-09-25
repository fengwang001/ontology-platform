package jstr

import "ontology/esc"

type ErrorKind string

const (
	KindControl       ErrorKind = "control character"
	KindUnknownEscape ErrorKind = "unknown escape"
	KindShortHex      ErrorKind = "short unicode escape"
	KindMissingQuote  ErrorKind = "missing closing quote"
	KindTrailingBytes ErrorKind = "trailing bytes"
	KindInvalidUTF8   ErrorKind = "invalid UTF-8"
	KindSurrogate     ErrorKind = "invalid surrogate pair"
)

type DecodeError struct {
	Kind   ErrorKind
	Offset int
}

type InvalidUTF8Error struct{}

type Decoder struct {
	buf       []byte
	offset    int
	inspected int
}

func Decode(lit []byte) (string, error) {
	return "", nil
}

func Encode(s string) ([]byte, error) {
	return nil, nil
}

func NewDecoder() *Decoder {
	return &Decoder{}
}

func (d *Decoder) Write(p []byte) (int, error) {
	return len(p), nil
}

func (d *Decoder) Close() (string, error) {
	return "", nil
}

func (d *Decoder) Inspected() int {
	return d.inspected
}

func (e DecodeError) Error() string {
	return string(e.Kind)
}

func (InvalidUTF8Error) Error() string {
	return "jstr: invalid UTF-8"
}

var _ = esc.Simple
