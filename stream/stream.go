// Package stream provides strict, streaming Base64 encoding and decoding.
package stream

import "ontology/b64"

var groupErr = []error{nil, b64.ErrInvalidChar, b64.ErrPadding, b64.ErrNonCanonical}

type Decoder struct {
	mime, pendCR, ended bool
	limit, gn           int
	out                 []byte
	group               [4]byte
	off, checked        int64
	err                 error
}

// NewDecoder: mime allows newlines between groups; limit > 0 caps output.
func NewDecoder(mime bool, limit int) *Decoder { return &Decoder{mime: mime, limit: limit} }

func (d *Decoder) Checked() int64 { return d.checked }
func (d *Decoder) Output() []byte { return d.out }

// Write feeds p into the decoder byte by byte, never rescanning.
func (d *Decoder) Write(p []byte) (int, error) {
	if d.err != nil {
		return 0, d.err
	}
	for _, c := range p {
		d.checked++
		if err := d.step(c); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

func (d *Decoder) step(c byte) error {
	off := d.off
	d.off++
	if d.pendCR {
		d.pendCR = false
		if c != '\n' {
			return d.fail(b64.ErrNewline, off-1)
		}
		return nil
	}
	if c == '\r' || c == '\n' {
		if !d.mime {
			return d.fail(b64.ErrInvalidChar, off)
		}
		if d.gn != 0 {
			return d.fail(b64.ErrNewline, off)
		}
		d.pendCR = c == '\r'
		return nil
	}
	if d.ended {
		return d.fail(b64.ErrPadding, off)
	}
	d.group[d.gn] = c
	d.gn++
	if d.gn < 4 {
		return nil
	}
	d.gn = 0
	return d.emit()
}

func (d *Decoder) emit() error {
	var b [3]byte
	n, pos, k := b64.DecodeGroup(d.group, &b)
	base := d.off - 4
	if k != b64.OK {
		return d.fail(groupErr[k], base+int64(pos))
	}
	if d.limit > 0 && len(d.out)+n > d.limit {
		return d.fail(b64.ErrLimit, base)
	}
	d.out = append(d.out, b[:n]...)
	d.ended = n < 3
	return nil
}

func (d *Decoder) Close() error {
	switch {
	case d.err != nil:
		return d.err
	case d.pendCR:
		return d.fail(b64.ErrNewline, d.off-1)
	case d.gn != 0:
		return d.fail(b64.ErrLength, d.off)
	}
	d.err = b64.ErrClosed
	return nil
}

func (d *Decoder) fail(kind error, off int64) error {
	d.err = &b64.Error{Kind: kind, Off: off}
	return d.err
}

type Encoder struct {
	mime, done bool
	col, np    int
	pend       [3]byte
	out        []byte
}

// NewEncoder: mime inserts CRLF every 76 characters, never after the last line.
func NewEncoder(mime bool) *Encoder { return &Encoder{mime: mime} }

// Write buffers and encodes p in 3-byte chunks.
func (e *Encoder) Write(p []byte) (int, error) {
	if e.done {
		return 0, b64.ErrClosed
	}
	for _, c := range p {
		e.pend[e.np] = c
		e.np++
		if e.np == 3 {
			e.emit(e.pend[:])
			e.np = 0
		}
	}
	return len(p), nil
}

func (e *Encoder) emit(b []byte) {
	if e.mime && e.col == 76 {
		e.out = append(e.out, '\r', '\n')
		e.col = 0
	}
	e.out = b64.EncodeGroup(e.out, b...)
	e.col += 4
}

// Close flushes the final partial chunk with padding.
func (e *Encoder) Close() error {
	if e.done {
		return b64.ErrClosed
	}
	e.done = true
	if e.np > 0 {
		e.emit(e.pend[:e.np])
	}
	return nil
}

// Output returns the encoded bytes.
func (e *Encoder) Output() []byte { return e.out }
