// Package stream provides strict, streaming Base64 encoding and decoding.
package stream

import (
	"errors"
	"fmt"
	"ontology/b64"
)

// Sentinels classifying stream-level failures, always wrapped in *Error.
var (
	ErrLength  = errors.New("stream: length not a multiple of 4")
	ErrNewline = errors.New("stream: misplaced newline")
	ErrLimit   = errors.New("stream: output limit exceeded")
	ErrClosed  = errors.New("stream: already closed")
)

// Error is a decode failure; Off is the byte offset in the whole input.
type Error struct {
	Kind error
	Off  int64
}

func (e *Error) Error() string { return fmt.Sprintf("%s at offset %d", e.Kind, e.Off) }
func (e *Error) Unwrap() error { return e.Kind }

// Decoder is a strict streaming Base64 decoder. The unexported input offset
// doubles as the counter of examined bytes: every input byte is seen once.
type Decoder struct {
	mime, final, cr, done bool
	limit, n              int
	out                   []byte
	off                   int64
	buf                   [4]byte
	err                   error
}

// NewDecoder: mime allows newlines between groups; limit < 0 means no cap.
func NewDecoder(mime bool, limit int) *Decoder { return &Decoder{mime: mime, limit: limit} }

// Checked reports how many input bytes were examined (never rescanned).
func (d *Decoder) Checked() int64 { return d.off }
func (d *Decoder) Output() []byte { return d.out }
func (d *Decoder) Write(p []byte) (int, error) {
	if d.done {
		if d.err == nil {
			d.err = &Error{ErrClosed, d.off}
		}
		return 0, d.err
	}
	for i, c := range p {
		if err := d.step(c); err != nil {
			return i, err
		}
	}
	return len(p), nil
}
func (d *Decoder) step(c byte) error {
	off := d.off
	d.off++
	if d.cr {
		d.cr = false
		if c != '\n' {
			return &Error{ErrNewline, off - 1}
		}
		return nil
	}
	if d.final {
		return &Error{b64.ErrPadding, off}
	}
	switch {
	case c == '\r' || c == '\n':
		if !d.mime || d.n != 0 {
			return &Error{ErrNewline, off}
		}
		d.cr = c == '\r'
	case c == '=' || b64.Valid(c):
		d.buf[d.n] = c
		if d.n++; d.n == 4 {
			d.n = 0
			return d.group(off - 3)
		}
	default:
		return &Error{b64.ErrChar, off}
	}
	return nil
}
func (d *Decoder) group(start int64) error {
	var tmp [3]byte
	n, padded, err := b64.DecodeGroup(d.buf[:], tmp[:])
	if err != nil {
		ge := err.(*b64.Error)
		return &Error{ge.Kind, start + int64(ge.Pos)}
	}
	if d.limit >= 0 && len(d.out)+n > d.limit {
		d.err = &Error{ErrLimit, start}
		d.done = true
		return d.err
	}
	d.out = append(d.out, tmp[:n]...)
	d.final = padded
	return nil
}

// Close finishes decoding and validates the stream tail.
func (d *Decoder) Close() error {
	if !d.done {
		d.done = true
		if d.cr {
			d.err = &Error{ErrNewline, d.off - 1}
		} else if d.n != 0 {
			d.err = &Error{ErrLength, d.off}
		}
	}
	return d.err
}

// Encoder is a streaming Base64 encoder.
type Encoder struct {
	mime, closed bool
	n, col       int
	out          []byte
	buf          [3]byte
}

// NewEncoder: mime wraps lines at 76 chars with CRLF, no trailing newline.
func NewEncoder(mime bool) *Encoder { return &Encoder{mime: mime} }
func (e *Encoder) Output() []byte   { return e.out }
func (e *Encoder) Write(p []byte) (int, error) {
	if e.closed {
		return 0, ErrClosed
	}
	for _, b := range p {
		e.buf[e.n] = b
		if e.n++; e.n == 3 {
			e.emit(e.buf[:])
			e.n = 0
		}
	}
	return len(p), nil
}
func (e *Encoder) emit(src []byte) {
	var g [4]byte
	b64.EncodeGroup(g[:], src)
	if e.mime && e.col == 76 {
		e.out = append(e.out, '\r', '\n')
		e.col = 0
	}
	e.out = append(e.out, g[:]...)
	e.col += 4
}

// Close flushes the final partial block with padding.
func (e *Encoder) Close() error {
	if e.closed {
		return ErrClosed
	}
	e.closed = true
	if e.n > 0 {
		e.emit(e.buf[:e.n])
	}
	return nil
}
