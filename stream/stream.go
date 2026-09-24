package stream

import (
	"errors"
	"fmt"
	"ontology/b64"
)

var (
	ErrInvalidChar  = b64.ErrInvalidChar
	ErrNonCanonical = b64.ErrNonCanonical
	ErrPadding      = b64.ErrPadding
	ErrLength       = errors.New("stream: input length not a multiple of 4")
	ErrNewline      = errors.New("stream: misplaced newline")
	ErrLimit        = errors.New("stream: output limit exceeded")
	ErrClosed       = errors.New("stream: already closed")
)

type Error struct {
	Kind error
	Off  int64
}

func (e *Error) Error() string { return fmt.Sprintf("%v at offset %d", e.Kind, e.Off) }
func (e *Error) Unwrap() error { return e.Kind }

type Decoder struct {
	mime, final, pendCR bool
	max, glen           int
	out                 []byte
	group               [4]byte
	off, checked, crOff int64
	err                 error
}

func NewDecoder(mime bool, max int) *Decoder { return &Decoder{mime: mime, max: max} }
func (d *Decoder) Output() []byte            { return d.out }
func (d *Decoder) Checked() int64            { return d.checked }

func (d *Decoder) fail(kind error, off int64) error {
	d.err = &Error{Kind: kind, Off: off}
	return d.err
}

func (d *Decoder) Write(p []byte) (int, error) {
	if d.err != nil {
		return 0, d.err
	}
	for i, c := range p {
		d.checked++
		cur := d.off
		d.off++
		if d.pendCR && c != '\n' {
			return i, d.fail(ErrNewline, d.crOff)
		}
		d.pendCR = false
		switch {
		case c == '\r' || c == '\n':
			if !d.mime || d.glen != 0 {
				return i, d.fail(ErrNewline, cur)
			}
			d.pendCR, d.crOff = c == '\r', cur
		case c == '=' || b64.Valid(c):
			if d.final {
				return i, d.fail(ErrPadding, cur)
			}
			d.group[d.glen] = c
			if d.glen++; d.glen == 4 {
				if err := d.flush(); err != nil {
					return i, err
				}
			}
		default:
			return i, d.fail(ErrInvalidChar, cur)
		}
	}
	return len(p), nil
}

func (d *Decoder) flush() error {
	out, n, gerr := b64.DecodeGroup(d.group)
	d.glen = 0
	if gerr != nil {
		return d.fail(gerr.Kind, d.off-4+int64(gerr.Idx))
	}
	if d.max > 0 && len(d.out)+n > d.max {
		return d.fail(ErrLimit, d.off-4)
	}
	d.out = append(d.out, out[:n]...)
	d.final = n < 3
	return nil
}

func (d *Decoder) Close() error {
	if d.err != nil {
		return d.err
	}
	if d.pendCR {
		return d.fail(ErrNewline, d.crOff)
	}
	if d.glen != 0 {
		return d.fail(ErrLength, d.off)
	}
	d.err = ErrClosed
	return nil
}

type Encoder struct {
	mime, closed bool
	out          []byte
	buf          [3]byte
	blen, col    int
}

func NewEncoder(mime bool) *Encoder { return &Encoder{mime: mime} }
func (e *Encoder) Output() []byte   { return e.out }

func (e *Encoder) Write(p []byte) (int, error) {
	if e.closed {
		return 0, ErrClosed
	}
	for _, c := range p {
		e.buf[e.blen] = c
		if e.blen++; e.blen == 3 {
			e.flush()
		}
	}
	return len(p), nil
}

func (e *Encoder) flush() {
	if e.mime && e.col == 76 {
		e.out = append(e.out, '\r', '\n')
		e.col = 0
	}
	g := b64.EncodeGroup(e.buf[:e.blen])
	e.out = append(e.out, g[:]...)
	e.col, e.blen = e.col+4, 0
}

func (e *Encoder) Close() error {
	if e.closed {
		return ErrClosed
	}
	e.closed = true
	if e.blen > 0 {
		e.flush()
	}
	return nil
}
