// Package stream is a strict-mode streaming Base64 encoder and decoder;
// half quanta, a pending '\r' and padding carry across Write boundaries.
package stream

import (
	"errors"

	"ontology/b64"
)

var (
	ErrLimit  = errors.New("stream: output exceeds limit") // past byte cap
	ErrClosed = errors.New("stream: closed")               // finished device
)

// Error wraps a b64 sentinel (or ErrLimit) with the whole-stream byte offset.
type Error struct {
	Err    error
	Offset int
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// Decoder reassembles quanta across Write calls; limit <= 0 is unlimited.
type Decoder struct {
	mime              bool
	limit             int
	buf               [4]byte
	n, qstart         int
	out               []byte
	checked, pos      int
	pendingCR         bool
	final, terminal   bool
}

// NewDecoder creates a strict decoder; line breaks are allowed only in mime.
func NewDecoder(mime bool, limit int) *Decoder {
	return &Decoder{mime: mime, limit: limit}
}

func (d *Decoder) fail(err error, off int) error {
	d.terminal = true
	return &Error{Err: err, Offset: off}
}

// Write consumes input; each byte is inspected exactly once (see Checked).
func (d *Decoder) Write(p []byte) (int, error) {
	if d.terminal {
		return 0, ErrClosed
	}
	for _, c := range p {
		off := d.pos
		d.pos++
		d.checked++
		if d.pendingCR {
			d.pendingCR = false
			if c != '\n' {
				return 0, d.fail(b64.ErrNewline, off-1)
			}
			c = '\n'
		}
		if c == '\r' {
			d.pendingCR = true
			continue
		}
		if c == '\n' {
			if !d.mime || d.n != 0 || d.final {
				return 0, d.fail(b64.ErrNewline, off)
			}
			continue
		}
		if d.final {
			return 0, d.fail(b64.ErrPadding, off)
		}
		if d.n == 0 {
			d.qstart = off
		}
		d.buf[d.n] = c
		d.n++
		if d.n == 4 {
			q, err := b64.DecodeQuantum(d.buf)
			if err != nil {
				return 0, d.fail(err, d.qstart)
			}
			if d.limit > 0 && len(d.out)+q.N > d.limit {
				return 0, d.fail(ErrLimit, d.qstart)
			}
			d.out = append(d.out, q.Data[:q.N]...)
			d.n = 0
			d.final = q.Final || d.final
		}
	}
	return len(p), nil
}

// Close finalizes, rejecting a trailing bare '\r' or an unfinished quantum.
func (d *Decoder) Close() error {
	if d.terminal {
		return ErrClosed
	}
	d.terminal = true
	switch {
	case d.pendingCR:
		return &Error{Err: b64.ErrNewline, Offset: d.pos - 1}
	case d.n != 0:
		return &Error{Err: b64.ErrLength, Offset: d.qstart}
	}
	return nil
}

// Output holds bytes emitted at complete quantum boundaries only.
func (d *Decoder) Output() []byte { return d.out }

// Checked counts inspected bytes; it equals the number of bytes fed in.
func (d *Decoder) Checked() int { return d.checked }

// Encoder groups bytes into quanta; mime inserts CRLF every 76 chars, no tail.
type Encoder struct {
	mime       bool
	in         [3]byte
	n, col     int
	out        []byte
	done       bool
}

// NewEncoder creates a streaming encoder.
func NewEncoder(mime bool) *Encoder { return &Encoder{mime: mime} }

func (e *Encoder) emit(c byte) {
	if e.mime && e.col == 76 {
		e.out = append(e.out, '\r', '\n')
		e.col = 0
	}
	e.out = append(e.out, c)
	e.col++
}

func (e *Encoder) emitChars(c [4]byte) {
	for i := 0; i < 4; i++ {
		e.emit(c[i])
	}
}

// Write buffers bytes and emits a quantum each time three bytes complete.
func (e *Encoder) Write(p []byte) (int, error) {
	if e.done {
		return 0, ErrClosed
	}
	for _, c := range p {
		e.in[e.n] = c
		e.n++
		if e.n == 3 {
			e.emitChars(b64.EncodeQuantum(e.in[:], 3).Chars)
			e.n = 0
		}
	}
	return len(p), nil
}

// Close flushes the final 1- or 2-byte quantum with canonical padding.
func (e *Encoder) Close() error {
	if e.done {
		return ErrClosed
	}
	e.done = true
	if e.n > 0 {
		e.emitChars(b64.EncodeQuantum(e.in[:e.n], e.n).Chars)
	}
	return nil
}

// Output returns the Base64 text produced so far.
func (e *Encoder) Output() []byte { return e.out }
