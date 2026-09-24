package stream

import (
	"errors"

	"ontology/b64"
)

var (
	ErrIllegalChar  = b64.ErrIllegalChar
	ErrNonCanonical = b64.ErrNonCanonical
	ErrPadding      = b64.ErrPadding
	ErrLength       = errors.New("stream: input length is not a multiple of 4")
	ErrNewline      = errors.New("stream: newline in illegal position")
	ErrLimit        = errors.New("stream: output exceeds configured limit")
)

type Error struct {
	Offset int
	Err    error
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

type Decoder struct {
	mime, cr, seenPad, closed, dead bool
	limit, gn, pos, examined        int
	out                             []byte
	group                           [4]byte
}

func NewDecoder(mime bool, limit int) *Decoder { return &Decoder{mime: mime, limit: limit} }

func (d *Decoder) Examined() int { return d.examined }

func (d *Decoder) Output() []byte                { return append([]byte(nil), d.out...) }
func (d *Decoder) fail(off int, err error) error { d.dead = true; return &Error{Offset: off, Err: err} }

func (d *Decoder) complete(off int) error {
	dec, err := b64.DecodeGroup(d.group)
	if err != nil {
		return d.fail(off, err)
	}
	if d.seenPad {
		return d.fail(off, ErrPadding)
	}
	if d.limit > 0 && len(d.out)+len(dec) > d.limit {
		return d.fail(off, ErrLimit)
	}
	d.out, d.gn = append(d.out, dec...), 0
	d.seenPad = d.group[2] == '=' || d.group[3] == '='
	return nil
}

func (d *Decoder) Write(p []byte) (int, error) {
	if d.dead || d.closed {
		return 0, d.fail(d.pos, ErrLength)
	}
	for _, c := range p {
		off := d.pos
		d.pos++
		d.examined++
		if d.cr {
			d.cr = false
			if c == '\n' {
				continue
			}
			return 0, d.fail(off, ErrNewline)
		}
		switch c {
		case '\r', '\n':
			if c == '\r' {
				d.cr = true
			}
			if !d.mime || d.gn != 0 {
				return 0, d.fail(off, ErrNewline)
			}
		default:
			if _, ok := b64.Value(c); c != '=' && !ok {
				return 0, d.fail(off, ErrIllegalChar)
			}
			d.group[d.gn] = c
			d.gn++
			if d.gn == 4 {
				if err := d.complete(off); err != nil {
					return 0, err
				}
			}
		}
	}
	return len(p), nil
}

func (d *Decoder) Close() error {
	if d.dead {
		return &Error{Offset: d.pos, Err: ErrLength}
	}
	d.closed = true
	if d.cr || d.gn != 0 {
		if d.cr {
			return d.fail(d.pos, ErrNewline)
		}
		return d.fail(d.pos, ErrLength)
	}
	return nil
}

type Encoder struct {
	mime, closed bool
	line, bn     int
	buf          [3]byte
	out          []byte
}

func NewEncoder(mime bool) *Encoder { return &Encoder{mime: mime} }

func (e *Encoder) emit(g [4]byte) {
	if e.mime && e.line == 76 {
		e.out, e.line = append(e.out, '\r', '\n'), 0
	}
	e.out, e.line = append(e.out, g[:]...), e.line+4
}

func (e *Encoder) Write(p []byte) (int, error) {
	if e.closed {
		return 0, errors.New("stream: write on closed encoder")
	}
	for _, c := range p {
		e.buf[e.bn] = c
		if e.bn++; e.bn == 3 {
			e.emit(b64.EncodeGroup(e.buf[:]))
			e.bn = 0
		}
	}
	return len(p), nil
}

func (e *Encoder) Close() error {
	if e.closed {
		return nil
	}
	e.closed = true
	if e.bn > 0 {
		e.emit(b64.EncodeGroup(e.buf[:e.bn]))
	}
	return nil
}

func (e *Encoder) Output() []byte { return append([]byte(nil), e.out...) }
