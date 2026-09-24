// Package stream provides strict-mode streaming Base64 encoders and decoders.
package stream

import (
	"errors"

	"ontology/b64"
)

// Distinguishable error categories, wrapped with ErrorAt to carry offsets.
var (
	ErrInvalidChar = b64.ErrInvalidChar
	ErrNonCanonical = b64.ErrNonCanonical
	ErrPadding      = b64.ErrPadding
	ErrLength       = errors.New("stream: input length is not a multiple of 4")
	ErrNewline      = errors.New("stream: illegal newline placement")
	ErrLimit        = errors.New("stream: decoded output exceeds limit")
)

// ErrorAt wraps a category error with its byte offset in the input stream.
type ErrorAt struct {
	Offset int
	Err    error
}

func (e *ErrorAt) Error() string { return e.Err.Error() }
func (e *ErrorAt) Unwrap() error { return e.Err }

type DecodeConfig struct {
	MIME  bool // accept \r\n / \n only between 4-char groups
	Limit int  // maximum decoded output bytes; <= 0 means unlimited
}

// Decoder incrementally decodes strict Base64; after any error it is terminal.
type Decoder struct {
	mime     bool
	limit    int
	out      []byte
	group    [4]byte
	n        int   // chars accumulated in the current group
	cr       bool  // a '\r' is pending, awaiting '\n'
	padded   bool  // a padding-bearing final group was emitted
	off      int   // absolute input offset
	closed   bool
	err      error
	examined int // counts every input byte exactly once (no rescanning)
}

// NewDecoder creates a decoder from cfg.
func NewDecoder(cfg DecodeConfig) *Decoder {
	return &Decoder{mime: cfg.MIME, limit: cfg.Limit}
}

func (d *Decoder) Output() []byte { return d.out }

// Examined returns the count of input bytes examined exactly once.
func (d *Decoder) Examined() int { return d.examined }

func (d *Decoder) fail(off int, err error) error {
	d.err = &ErrorAt{Offset: off, Err: err}
	return d.err
}

// Write feeds more Base64 bytes; n is the failing byte's index within p.
func (d *Decoder) Write(p []byte) (int, error) {
	if d.closed {
		return len(p), nil
	}
	if d.err != nil {
		return 0, d.err
	}
	for i, c := range p {
		d.off++
		d.examined++
		pos := d.off - 1

		if d.cr {
			if c != '\n' {
				return i, d.fail(pos, ErrNewline)
			}
			if d.n != 0 {
				return i, d.fail(pos, ErrNewline)
			}
			d.cr = false
			continue
		}

		switch {
		case c == '\r':
			if !d.mime || d.n != 0 {
				return i, d.fail(pos, ErrNewline)
			}
			d.cr = true
		case c == '\n':
			if !d.mime || d.n != 0 {
				return i, d.fail(pos, ErrNewline)
			}
		case c == b64.Padding:
			if d.padded || d.n < 2 {
				return i, d.fail(pos, ErrPadding)
			}
			d.group[d.n] = c
			d.n++
		default:
			if _, ok := b64.Value(c); !ok {
				return i, d.fail(pos, ErrInvalidChar)
			}
			d.group[d.n] = c
			d.n++
		}

		if d.n == 4 {
			dec, m, err := b64.DecodeGroup(d.group)
			if err != nil {
				return i, d.fail(pos, err)
			}
			if d.limit > 0 && len(d.out)+m > d.limit {
				return i, d.fail(pos, ErrLimit)
			}
			d.out = append(d.out, dec[:m]...)
			d.n = 0
			if d.group[2] == b64.Padding || d.group[3] == b64.Padding {
				d.padded = true
			}
		}
	}
	return len(p), nil
}

// Close validates the stream tail. The decoder becomes unusable afterward.
func (d *Decoder) Close() error {
	if d.err != nil {
		return d.err
	}
	if d.cr {
		return d.fail(d.off-1, ErrNewline)
	}
	if d.n != 0 {
		return d.fail(d.off, ErrLength)
	}
	d.closed = true
	return nil
}

// Decode decodes all of src in one pass using cfg.
func Decode(src []byte, cfg DecodeConfig) ([]byte, error) {
	d := NewDecoder(cfg)
	if _, err := d.Write(src); err != nil {
		return nil, err
	}
	if err := d.Close(); err != nil {
		return nil, err
	}
	return d.Output(), nil
}
