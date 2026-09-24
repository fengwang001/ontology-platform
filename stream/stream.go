// Package stream provides strict, incremental RFC 4648 base64 codecs.
// Only canonical encodings are accepted: the unused low bits of the
// final data character must be zero; padding occurs only at stream end.
package stream

import (
	"fmt"

	"ontology/b64"
)

// Kind identifies which of the five required error classes occurred.
type Kind int

const (
	IllegalChar Kind = iota + 1
	NonCanonical
	Padding
	BadLength
	BadNewline
	OutputLimited
)

var kindText = map[Kind]string{
	IllegalChar: "illegal character", NonCanonical: "non-canonical tail",
	Padding: "padding position error", BadLength: "length is not a multiple of 4",
	BadNewline: "newline not allowed here", OutputLimited: "output limit exceeded",
}

// Error carries a distinguishable Kind and the byte Offset in the input.
type Error struct {
	Kind   Kind
	Offset int64
}

func (e *Error) Error() string { return fmt.Sprintf("%s at offset %d", kindText[e.Kind], e.Offset) }

// Is lets errors.Is distinguish the five classes via sentinel errors.
var (
	ErrIllegalChar  = &Error{Kind: IllegalChar}
	ErrNonCanonical = &Error{Kind: NonCanonical}
	ErrPadding      = &Error{Kind: Padding}
	ErrLength       = &Error{Kind: BadLength}
	ErrNewline      = &Error{Kind: BadNewline}
	ErrOutputLimit  = &Error{Kind: OutputLimited}
)

func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Kind == e.Kind
}

type Decoder struct {
	mime, cr, pad, done bool
	limit               int64
	pos                 int
	checked, data       int64
	out                 []byte
	grp                 [4]byte
	err                 error
}

// NewDecoder creates a strict decoder. In mime mode "\n" or "\r\n" is
// accepted only between 4-character groups; limit < 0 means unlimited.
func NewDecoder(mime bool, limit int64) *Decoder { return &Decoder{mime: mime, limit: limit} }

// Write feeds input; every input byte is examined exactly once.
func (d *Decoder) Write(p []byte) (int, error) {
	if d.done {
		return 0, d.err
	}
	for i := range p {
		d.checked++
		if err := d.feed(p[i]); err != nil {
			d.done, d.err = true, err
			return i + 1, err
		}
	}
	return len(p), nil
}

// Close marks end of stream and validates the trailing partial group.
func (d *Decoder) Close() error {
	if !d.done {
		d.done = true
		if d.cr {
			d.err = &Error{BadNewline, d.checked - 1}
		} else if d.pos != 0 {
			d.err = &Error{BadLength, d.checked}
		}
	}
	return d.err
}

func (d *Decoder) Output() []byte { return d.out }
func (d *Decoder) Checked() int64 { return d.checked }
func (d *Decoder) Err() error     { return d.err }

func (d *Decoder) feed(c byte) error {
	off := d.checked - 1
	if d.cr {
		d.cr = false
		if c != '\n' {
			return &Error{BadNewline, off - 1}
		}
		return d.breakAt(off - 1)
	}
	switch c {
	case '\r':
		if !d.mime {
			return &Error{BadNewline, off}
		}
		d.cr = true
	case '\n':
		if !d.mime {
			return &Error{BadNewline, off}
		}
		return d.breakAt(off)
	case b64.Padding:
		if d.pos < 2 || d.pad {
			return &Error{Padding, off}
		}
		d.pad = true
	default:
		switch {
		case !b64.IsAlphabet(c):
			return &Error{IllegalChar, off}
		case d.pad:
			return &Error{Padding, off}
		default:
			d.data++
		}
	}
	d.grp[d.pos] = c
	if d.pos++; d.pos == 4 {
		return d.flush()
	}
	return nil
}

func (d *Decoder) breakAt(off int64) error {
	if d.pos != 0 || d.pad || d.data == 0 {
		return &Error{BadNewline, off}
	}
	return nil
}

func (d *Decoder) flush() error {
	dec, err := b64.DecodeGroup(d.grp)
	start := d.checked - 4
	if err != nil {
		if off, ok := d.nonCanonical(); ok {
			return &Error{NonCanonical, off}
		}
		return &Error{Padding, start}
	}
	if d.limit >= 0 && int64(len(d.out)+len(dec)) > d.limit {
		return &Error{OutputLimited, start}
	}
	d.out = append(d.out, dec...)
	if len(dec) < 3 {
		d.pad = true
	}
	d.pos = 0
	return nil
}

// nonCanonical reports the offending character when the padding shape
// is legal but the unused low bits are non-zero.
func (d *Decoder) nonCanonical() (int64, bool) {
	start := d.checked - 4
	switch {
	case d.grp[2] == b64.Padding:
		v, _ := b64.Value(d.grp[1])
		return start + 1, v&0x0F != 0
	case d.grp[3] == b64.Padding:
		v, _ := b64.Value(d.grp[2])
		return start + 2, v&0x03 != 0
	}
	return 0, false
}

type Encoder struct {
	mime, closed bool
	col          int
	out, tail    []byte
}

// NewEncoder creates an encoder; in mime mode CRLF follows every 76
// output characters except after the final line.
func NewEncoder(mime bool) *Encoder { return &Encoder{mime: mime} }

func (e *Encoder) Write(p []byte) (int, error) {
	e.tail = append(e.tail, p...)
	for len(e.tail) >= 3 {
		e.emit(b64.EncodeGroup(e.tail[:3]))
		e.tail = e.tail[3:]
	}
	return len(p), nil
}

func (e *Encoder) Close() error {
	if !e.closed {
		e.closed = true
		if len(e.tail) > 0 {
			e.emit(b64.EncodeGroup(e.tail))
			e.tail = nil
		}
	}
	return nil
}

func (e *Encoder) Output() []byte { return e.out }

func (e *Encoder) emit(g [4]byte) {
	for _, c := range g {
		if e.mime && e.col == 76 {
			e.out = append(e.out, '\r', '\n')
			e.col = 0
		}
		e.out = append(e.out, c)
		e.col++
	}
}
