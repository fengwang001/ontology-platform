// Package stream provides strict, streaming Base64 decoding and encoding.
package stream

import (
	"errors"
	"fmt"

	"ontology/b64"
)

// Sentinel errors distinguishing the strict-decoding failure classes.
var (
	ErrInvalidChar  = errors.New("invalid character")
	ErrNonCanonical = errors.New("non-canonical trailing bits")
	ErrPadding      = errors.New("misplaced padding")
	ErrLength       = errors.New("input length not a multiple of 4")
	ErrNewline      = errors.New("misplaced newline")
	ErrOverflow     = errors.New("output limit exceeded")
	ErrClosed       = errors.New("stream already closed")
)

// Error is a decoding failure: a sentinel plus absolute byte offset.
type Error struct {
	Err    error
	Offset int64
}

func (e *Error) Error() string { return fmt.Sprintf("stream: %v at byte %d", e.Err, e.Offset) }
func (e *Error) Unwrap() error { return e.Err }

type Decoder struct {
	mime, final, cr, done bool
	max                   int
	out, grp              []byte
	off, checked          int64
	err                   error
}

// NewDecoder: mime allows CRLF/LF only between groups; max < 0 is unlimited.
func NewDecoder(mime bool, max int) *Decoder { return &Decoder{mime: mime, max: max} }
func (d *Decoder) Output() []byte            { return d.out }
func (d *Decoder) Checked() int64            { return d.checked }
func (d *Decoder) Write(p []byte) (int, error) {
	if d.done {
		if d.err != nil {
			return 0, d.err
		}
		return 0, ErrClosed
	}
	for i, c := range p {
		d.checked++
		if d.err = d.step(c); d.err != nil {
			d.done = true
			return i, d.err
		}
	}
	return len(p), nil
}
func (d *Decoder) Close() error {
	if d.done {
		return d.err
	}
	d.done = true
	if d.cr {
		d.err = &Error{ErrNewline, d.off}
	} else if len(d.grp) > 0 {
		d.err = &Error{ErrLength, d.off}
	}
	return d.err
}
func (d *Decoder) step(c byte) error {
	off := d.off
	d.off++
	if d.cr || c == '\r' || c == '\n' {
		ok := d.cr && c == '\n' || !d.cr && d.mime && len(d.grp) == 0
		if !ok {
			return &Error{ErrNewline, off}
		}
		d.cr = c == '\r'
		return nil
	}
	if c != '=' && b64.Value(c) < 0 {
		return &Error{ErrInvalidChar, off}
	}
	if d.final {
		return &Error{ErrPadding, off}
	}
	d.grp = append(d.grp, c)
	if len(d.grp) == 4 {
		return d.flush()
	}
	return nil
}
func (d *Decoder) flush() error {
	n, kind, idx := b64.CheckGroup(d.grp)
	start := d.off - int64(len(d.grp))
	if kind != b64.OK {
		return &Error{[]error{nil, ErrInvalidChar, ErrPadding, ErrNonCanonical}[kind], start + int64(idx)}
	}
	if d.max >= 0 && len(d.out)+n > d.max {
		return &Error{ErrOverflow, start}
	}
	b := b64.Unpack(d.grp)
	d.out = append(d.out, b[:n]...)
	d.final = n < 3
	d.grp = d.grp[:0]
	return nil
}

type Encoder struct {
	mime, closed bool
	out, buf     []byte
	col          int
}

// NewEncoder: mime wraps output in CRLF every 76 chars, never at the end.
func NewEncoder(mime bool) *Encoder { return &Encoder{mime: mime} }
func (e *Encoder) Output() []byte   { return e.out }
func (e *Encoder) Write(p []byte) (int, error) {
	if e.closed {
		return 0, ErrClosed
	}
	for _, b := range p {
		e.buf = append(e.buf, b)
		if len(e.buf) == 3 {
			e.emit(e.buf)
			e.buf = e.buf[:0]
		}
	}
	return len(p), nil
}
func (e *Encoder) Close() error {
	if e.closed {
		return ErrClosed
	}
	if len(e.buf) > 0 {
		e.emit(e.buf)
		e.buf = nil
	}
	e.closed = true
	return nil
}
func (e *Encoder) emit(src []byte) {
	if e.mime && e.col == 76 {
		e.out = append(e.out, '\r', '\n')
		e.col = 0
	}
	e.out = b64.AppendEncode(e.out, src)
	e.col += 4
}
