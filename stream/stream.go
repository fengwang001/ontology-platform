// Package stream implements streaming strict Base64 codec.
package stream

import "errors"

type Kind int

const (
	KindIllegalChar Kind = iota
	KindNonCanonical
	KindPadding
	KindLength
	KindNewline
)

var (
	ErrIllegalChar = errors.New("stream: illegal character")
	ErrNonCanonical = errors.New("stream: non-canonical tail group")
	ErrPadding = errors.New("stream: padding in wrong position")
	ErrLength = errors.New("stream: input length is not a multiple of 4")
	ErrNewline = errors.New("stream: newline in illegal position")
	ErrLimit = errors.New("stream: output size limit exceeded")
)

// Error carries error category and byte offset in the full input stream.
type Error struct {
	Kind   Kind
	Offset int
	Err    error
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

type Decoder struct{}

func NewDecoder(mime bool, limit int) *Decoder { return &Decoder{} }

func (d *Decoder) Write(p []byte) (int, error) { return len(p), nil }
func (d *Decoder) Close() error                { return nil }
func (d *Decoder) Output() []byte              { return nil }

type Encoder struct{}

func NewEncoder(mime bool) *Encoder { return &Encoder{} }

func (e *Encoder) Write(p []byte) (int, error) { return len(p), nil }
func (e *Encoder) Close() error                { return nil }
func (e *Encoder) Output() []byte              { return nil }
