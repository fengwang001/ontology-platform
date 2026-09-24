// Package stream provides strict, streaming Base64 encoding and decoding
// built on the single-group primitives of package b64.
package stream

import (
	"errors"
	"fmt"

	"ontology/b64"
)

// Kind classifies stream decoding failures.
type Kind int

const (
	KindChar     Kind = iota // character outside the alphabet
	KindCanon                // non-zero trailing bits in the final group
	KindPad                  // padding misplaced, misshaped, or not at stream end
	KindLength               // length not a multiple of 4 at stream end
	KindNewline              // newline at an illegal position, or MIME disabled
	KindOverflow             // decoded output would exceed the configured limit
)

// Error is a decoding failure; Off is the byte offset in the whole stream.
type Error struct {
	Kind Kind
	Off  int64
}

func (e *Error) Error() string {
	name := [...]string{"invalid character", "non-canonical tail", "misplaced padding",
		"length not multiple of 4", "misplaced newline", "output limit exceeded"}[e.Kind]
	return fmt.Sprintf("stream: %s at offset %d", name, e.Off)
}

// Is matches any *Error with the same Kind, so errors.Is(err, ErrPadding)
// classifies failures regardless of offset.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Kind == e.Kind
}

// Sentinel values for errors.Is classification.
var (
	ErrInvalidChar  = &Error{Kind: KindChar}
	ErrNonCanonical = &Error{Kind: KindCanon}
	ErrPadding      = &Error{Kind: KindPad}
	ErrLength       = &Error{Kind: KindLength}
	ErrNewline      = &Error{Kind: KindNewline}
	ErrOverflow     = &Error{Kind: KindOverflow}
)

// Decoder is a strict streaming Base64 decoder. Bytes are examined exactly
// once; checked counts examinations. limit <= 0 means unlimited output.
type Decoder struct {
	mime, pad, cr, nl bool
	limit, off        int64
	checked           int64
	out               []byte
	grp               [4]byte
	gn                int
	err               error
}

// NewDecoder returns a Decoder. mime allows CR LF / LF between groups.
func NewDecoder(mime bool, limit int64) *Decoder { return &Decoder{mime: mime, limit: limit} }

// Write feeds p, returning the number of bytes consumed before any error.
func (d *Decoder) Write(p []byte) (int, error) {
	if d.err != nil {
		return 0, d.err
	}
	for i, c := range p {
		d.checked++
		if err := d.step(c); err != nil {
			d.err = err
			return i, err
		}
		d.off++
	}
	return len(p), nil
}

func (d *Decoder) step(c byte) error {
	if d.cr {
		d.cr = false
		if c != '\n' {
			return &Error{KindNewline, d.off}
		}
		return nil
	}
	if c == '\r' || c == '\n' {
		if !d.mime || d.gn != 0 {
			return &Error{KindNewline, d.off}
		}
		if d.pad {
			return &Error{KindPad, d.off}
		}
		d.cr, d.nl = c == '\r', true
		return nil
	}
	d.nl = false
	if d.pad {
		return &Error{KindPad, d.off}
	}
	d.grp[d.gn], d.gn = c, d.gn+1
	if d.gn == 4 {
		d.gn = 0
		return d.emit()
	}
	return nil
}

func (d *Decoder) emit() error {
	out, err := b64.DecodeGroup(d.grp)
	if err != nil {
		ge := err.(*b64.Error)
		return &Error{[...]Kind{KindChar, KindPad, KindCanon}[ge.Kind], d.off - 3 + int64(ge.Pos)}
	}
	if len(out) < 3 {
		d.pad = true
	}
	if d.limit > 0 && int64(len(d.out)+len(out)) > d.limit {
		return &Error{KindOverflow, d.off - 3}
	}
	d.out = append(d.out, out...)
	return nil
}

// Close finishes the stream, validating the final state.
func (d *Decoder) Close() error {
	if d.err != nil {
		return d.err
	}
	switch {
	case d.cr, d.nl:
		d.err = &Error{KindNewline, d.off - 1}
	case d.gn != 0:
		d.err = &Error{KindLength, d.off}
	}
	return d.err
}

// Output returns the decoded bytes so far.
func (d *Decoder) Output() []byte { return d.out }

// Encoder is a streaming Base64 encoder. With mime, lines wrap at 76
// characters with CR LF and no trailing newline.
type Encoder struct {
	mime, done bool
	buf        [3]byte
	n, col     int
	out        []byte
}

// NewEncoder returns an Encoder.
func NewEncoder(mime bool) *Encoder { return &Encoder{mime: mime} }

// Write encodes p, buffering any partial 3-byte block.
func (e *Encoder) Write(p []byte) (int, error) {
	if e.done {
		return 0, errors.New("stream: write on closed encoder")
	}
	for _, c := range p {
		e.buf[e.n] = c
		e.n++
		if e.n == 3 {
			e.emit(3)
		}
	}
	return len(p), nil
}

func (e *Encoder) emit(n int) {
	if e.mime && e.col == 76 {
		e.out = append(e.out, '\r', '\n')
		e.col = 0
	}
	e.out = b64.AppendEncode(e.out, e.buf[:n])
	e.col += 4
	e.n = 0
}

// Close flushes the final partial block with padding.
func (e *Encoder) Close() error {
	if !e.done {
		e.done = true
		if e.n > 0 {
			e.emit(e.n)
		}
	}
	return nil
}

// Output returns the encoded bytes so far.
func (e *Encoder) Output() []byte { return e.out }
