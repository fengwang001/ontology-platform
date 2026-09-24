// Package stream provides strict, streaming Base64 encoders and decoders.
// A byte is examined exactly once, so behavior is independent of where the
// input is split across Write calls.
package stream

import (
	"errors"

	"ontology/b64"
)

// Sentinel errors classify the five distinct strict-mode failure classes.
var (
	ErrIllegalChar = errors.New("stream: illegal base64 character")
	ErrCanonical   = errors.New("stream: non-canonical tail")
	ErrPadding     = errors.New("stream: padding outside terminal position")
	ErrLength      = errors.New("stream: input length is not a multiple of 4")
	ErrNewline     = errors.New("stream: newline in illegal position")
	ErrLimit       = errors.New("stream: output length limit exceeded")
)

// Error reports a failure class and the byte offset within the full input.
type Error struct {
	Cause  error
	Offset int
}

func (e *Error) Error() string { return e.Cause.Error() }
func (e *Error) Unwrap() error { return e.Cause }

type state int

const (
	sRun state = iota
	sCR
	sEnded
	sFailed
)

// Decoder is an io.Writer-like strict streaming Base64 decoder.
type Decoder struct {
	mime       bool
	limit      int // max output bytes, -1 disables the limit
	group      [4]byte
	n          int
	groupStart int
	offset     int
	st         state
	firstErr   *Error
	out        []byte

	// checked counts input bytes examined; each input byte is counted once.
	checked int
}

// NewDecoder creates a decoder. MIME permits CRLF/LF only between 4-char
// groups; limit caps decoded output bytes (-1 for unlimited).
func NewDecoder(mime bool, limit int) *Decoder {
	return &Decoder{mime: mime, limit: limit}
}

// Checked returns the total number of input bytes examined so far.
func (d *Decoder) Checked() int { return d.checked }

// Output returns the fully decoded bytes produced so far.
func (d *Decoder) Output() []byte { return d.out }

func (d *Decoder) fail(cause error, offset int) (int, error) {
	d.st = sFailed
	d.firstErr = &Error{Cause: cause, Offset: offset}
	return len(d.out), d.firstErr
}

func (d *Decoder) groupErr(ge *b64.GroupError) (int, error) {
	return d.fail(mapCause(ge.Cause), d.groupStart+ge.Index)
}

func mapCause(cause error) error {
	switch cause {
	case b64.ErrIllegalChar:
		return ErrIllegalChar
	case b64.ErrCanonical:
		return ErrCanonical
	default:
		return ErrPadding
	}
}

// Write feeds a chunk of encoded input.
func (d *Decoder) Write(p []byte) (int, error) {
	if d.st == sFailed {
		return 0, d.firstErr
	}
	for _, c := range p {
		d.checked++
		switch c {
		case '\r':
			if !d.mime || d.n != 0 || d.st == sEnded {
				return d.fail(newlineOrPadding(d.st), d.offset)
			}
			d.st, d.offset = sCR, d.offset+1
			continue
		case '\n':
			if d.st == sCR {
				d.st, d.offset = sRun, d.offset+1
				continue
			}
			if !d.mime || d.n != 0 || d.st == sEnded {
				return d.fail(newlineOrPadding(d.st), d.offset)
			}
			d.offset++
			continue
		}
		if d.st == sCR || d.st == sEnded {
			off := d.offset
			if d.st == sCR {
				off--
			}
			return d.fail(newlineOrPadding(d.st), off)
		}
		d.group[d.n] = c
		d.n++
		d.offset++
		if d.n == 4 {
			dec, err := b64.DecodeGroup(d.group)
			if err != nil {
				return d.groupErr(err.(*b64.GroupError))
			}
			if d.limit >= 0 && len(d.out)+len(dec) > d.limit {
				return d.fail(ErrLimit, d.groupStart)
			}
			d.out = append(d.out, dec...)
			d.n = 0
			d.groupStart = d.offset
			if d.group[2] == '=' || d.group[3] == '=' {
				d.st = sEnded
			}
		}
	}
	return len(d.out), nil
}

func newlineOrPadding(st state) error {
	if st == sEnded {
		return ErrPadding
	}
	return ErrNewline
}

// Close marks end of input and validates the trailing partial group.
func (d *Decoder) Close() error {
	switch d.st {
	case sFailed:
		return d.firstErr
	case sCR:
		_, err := d.fail(ErrNewline, d.groupStart)
		return err
	case sEnded:
		return nil
	}
	if d.n != 0 {
		_, err := d.fail(ErrLength, d.groupStart)
		return err
	}
	d.st = sEnded
	return nil
}

// Encoder is an io.Writer-like streaming Base64 encoder.
type Encoder struct {
	mime bool
	buf  []byte
	out  []byte
	col  int
}

// NewEncoder creates an encoder; with MIME it inserts CRLF every 76 chars.
func NewEncoder(mime bool) *Encoder { return &Encoder{mime: mime} }

// Output returns the encoded bytes produced so far.
func (e *Encoder) Output() []byte { return e.out }

// Write feeds raw bytes to encode.
func (e *Encoder) Write(p []byte) (int, error) {
	e.buf = append(e.buf, p...)
	for len(e.buf) >= 3 {
		e.emit()
	}
	return len(p), nil
}

func (e *Encoder) emit() {
	var g [4]byte
	b64.EncodeGroup(g[:], e.buf[:3])
	e.buf = e.buf[3:]
	e.out = append(e.out, g[:]...)
	e.col += 4
	if e.mime && e.col == 76 && len(e.buf) >= 3 {
		e.out = append(e.out, '\r', '\n')
		e.col = 0
	}
}

// Close flushes the final partial group with canonical padding.
func (e *Encoder) Close() error {
	if len(e.buf) > 0 {
		if e.mime && e.col >= 76 {
			e.out = append(e.out, '\r', '\n')
		}
		var g [4]byte
		b64.EncodeGroup(g[:], e.buf)
		e.out = append(e.out, g[:]...)
		e.buf = nil
	}
	return nil
}
