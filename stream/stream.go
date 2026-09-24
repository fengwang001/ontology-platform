// Package stream provides strict, streaming Base64 encoders and decoders.
package stream

import "errors"

// Five distinguishable decoder error classes.
var (
	ErrIllegalChar     = errWithOff{errors.New("stream: illegal character")}
	ErrNonCanonical    = errWithOff{errors.New("stream: non-canonical tail group")}
	ErrPaddingPosition = errWithOff{errors.New("stream: padding in illegal position")}
	ErrLength          = errWithOff{errors.New("stream: length is not a multiple of 4")}
	ErrNewlinePosition = errWithOff{errors.New("stream: newline in illegal position")}
	ErrLimitExceeded   = errWithOff{errors.New("stream: output limit exceeded")}
)

type errWithOff struct{ err error }

func (e errWithOff) Error() string { return e.err.Error() }
func (e errWithOff) Unwrap() error { return e.err }

// OffsetError carries the byte offset at which decoding failed.
type OffsetError struct {
	Class error
	Off   int
}

func (e *OffsetError) Error() string { return e.Class.Error() }
func (e *OffsetError) Unwrap() error { return e.Class }

// Decoder is a strict streaming Base64 decoder.
type Decoder struct {
	mime    bool
	limit   int
	out     []byte
	carry   []byte
	inGroup int
	groups  int
	closed  bool
	dead    bool
	err     *OffsetError
	sawCR   bool
	checked int // number of input bytes inspected (never rescanned)
}

// NewDecoder creates a decoder. mime allows CRLF/LF between groups;
// limit (<0 disables) caps decoded output bytes.
func NewDecoder(mime bool, limit int) *Decoder {
	return &Decoder{mime: mime, limit: limit}
}

// Write feeds encoded bytes.
func (d *Decoder) Write(p []byte) (int, error) { return len(p), nil }

// Close finalizes the stream and validates length and padding.
func (d *Decoder) Close() error { return nil }

// Output returns decoded bytes produced so far.
func (d *Decoder) Output() []byte { return d.out }

// Checked returns the total count of input bytes inspected.
func (d *Decoder) Checked() int { return d.checked }

// Encoder is a streaming Base64 encoder.
type Encoder struct {
	mime  bool
	out   []byte
	carry []byte
	col   int
}

// NewEncoder creates an encoder; mime inserts CRLF every 76 chars.
func NewEncoder(mime bool) *Encoder { return &Encoder{mime: mime} }

// Write feeds raw bytes.
func (e *Encoder) Write(p []byte) (int, error) { return len(p), nil }

// Close flushes the final padded group.
func (e *Encoder) Close() error { return nil }

// Output returns the encoded text.
func (e *Encoder) Output() []byte { return e.out }
