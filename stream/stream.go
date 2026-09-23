// Package stream provides strict, canonical, streaming base64 encoder and
// decoder over an arbitrary byte-slice chunk boundary. It depends only on b64.
package stream

import (
	"errors"

	"ontology/b64"
)

// Error categories. They are pairwise distinct and all wrap OffsetError.
var (
	ErrIllegalChar  = errors.New("stream: illegal character")
	ErrNonCanonical = errors.New("stream: non-canonical tail")
	ErrPadding      = errors.New("stream: padding position error")
	ErrLength       = errors.New("stream: input length is not a multiple of 4")
	ErrNewline      = errors.New("stream: newline position error")
	ErrLimit        = errors.New("stream: output limit exceeded")
	ErrTerminal     = errors.New("stream: decoder is in terminal state")
)

// OffsetError pairs a category sentinel with the byte offset in the whole
// input stream where the violation was detected.
type OffsetError struct {
	Cat    error
	Offset int
}

func (e *OffsetError) Error() string { return e.Cat.Error() }
func (e *OffsetError) Unwrap() error { return e.Cat }

// NewDecoder builds a strict streaming decoder. mime enables CRLF/LF between
// groups; limit<=0 means unbounded decoded output length.
func NewDecoder(mime bool, limit int) *Decoder {
	return &Decoder{mime: mime, limit: limit}
}

// Decoder consumes base64 across Write calls. Once Close succeeds, Output
// holds the decoded bytes. Any error moves it to a terminal state.
type Decoder struct {
	mime       bool
	limit      int
	group      [4]byte
	n          int
	ended      bool
	pendingCR  bool
	out        []byte
	offset     int
	terminal   bool
	checkCount int
}

func (d *Decoder) fail(cat error, off int) (int, error) {
	d.terminal = true
	return 0, &OffsetError{Cat: cat, Offset: off}
}

// Write appends one chunk. Each input byte is examined exactly once; bytes held
// in the partial-group buffer are never rescanned.
func (d *Decoder) Write(p []byte) (int, error) {
	if d.terminal {
		return 0, ErrTerminal
	}
	for i := 0; i < len(p); i++ {
		c := p[i]
		off := d.offset
		d.offset++
		d.checkCount++
		if d.pendingCR {
			d.pendingCR = false
			if c != '\n' {
				return d.fail(ErrNewline, off)
			}
			c = '\n' // treat the CR of CRLF as the newline marker offset
		}
		if c == '\r' {
			if !d.mime {
				return d.fail(ErrIllegalChar, off)
			}
			d.pendingCR = true
			continue
		}
		if c == '\n' {
			if !d.mime {
				return d.fail(ErrIllegalChar, off)
			}
			if d.n != 0 || d.ended {
				return d.fail(ErrNewline, off)
			}
			continue
		}
		if d.ended {
			cat := ErrPadding
			if _, ok := b64.Value(c); !ok && c != b64.Padding {
				cat = ErrIllegalChar
			}
			return d.fail(cat, off)
		}
		if _, ok := b64.Value(c); !ok && c != b64.Padding {
			return d.fail(ErrIllegalChar, off)
		}
		d.group[d.n] = c
		d.n++
		if d.n == 4 {
			if err := d.flushGroup(off); err != nil {
				return 0, err
			}
			d.n = 0
		}
	}
	return len(p), nil
}

func (d *Decoder) flushGroup(off int) error {
	dec, err := b64.DecodeGroup(d.group)
	if err != nil {
		cat := ErrPadding
		if errors.Is(err, b64.ErrIllegalChar) {
			cat = ErrIllegalChar
		} else if errors.Is(err, b64.ErrNonCanonical) {
			cat = ErrNonCanonical
		}
		_, e := d.fail(cat, off)
		return e
	}
	if d.limit > 0 && len(d.out)+len(dec) > d.limit {
		_, e := d.fail(ErrLimit, off)
		return e
	}
	d.out = append(d.out, dec...)
	if d.group[2] == b64.Padding || d.group[3] == b64.Padding {
		d.ended = true
	}
	return nil
}

// Close validates stream end: no dangling CR, no partial group. Empty input is
// legal and yields a zero-length output.
func (d *Decoder) Close() error {
	if d.terminal {
		return ErrTerminal
	}
	if d.pendingCR {
		_, err := d.fail(ErrNewline, d.offset-1)
		return err
	}
	if d.n != 0 {
		_, err := d.fail(ErrLength, d.offset)
		return err
	}
	d.terminal = true
	return nil
}

// Output returns decoded bytes after a successful Close.
func (d *Decoder) Output() []byte { return d.out }

// CheckCount returns the total number of input bytes examined exactly once.
func (d *Decoder) CheckCount() int { return d.checkCount }

// NewEncoder builds a streaming encoder. With mime true it inserts CRLF every
// 76 encoded characters; the final line never gets a trailing newline.
func NewEncoder(mime bool) *Encoder {
	return &Encoder{mime: mime}
}

// Encoder accumulates raw bytes and produces canonical base64 on Close.
type Encoder struct {
	mime    bool
	residue [3]byte
	held    int
	col     int
	out     []byte
}

func (e *Encoder) emitChar(c byte) {
	if e.mime && e.col == 76 {
		e.out = append(e.out, '\r', '\n')
		e.col = 0
	}
	e.out = append(e.out, c)
	e.col++
}

func (e *Encoder) emitGroup(g []byte) {
	for i := 0; i < 4; i++ {
		e.emitChar(g[i])
	}
}

// Write buffers raw bytes, encoding whole 3-byte groups immediately.
func (e *Encoder) Write(p []byte) (int, error) {
	n := len(p)
	if e.held > 0 {
		for len(p) > 0 && e.held < 3 {
			e.residue[e.held] = p[0]
			e.held++
			p = p[1:]
		}
		if e.held == 3 {
			e.emitGroup(b64.EncodeGroup(e.residue[:]))
			e.held = 0
		}
	}
	for len(p) >= 3 {
		e.emitGroup(b64.EncodeGroup(p[:3]))
		p = p[3:]
	}
	for len(p) > 0 {
		e.residue[e.held] = p[0]
		e.held++
		p = p[1:]
	}
	return n, nil
}

// Close flushes the final 1- or 2-byte tail with canonical padding.
func (e *Encoder) Close() error {
	if e.held > 0 {
		e.emitGroup(b64.EncodeGroup(e.residue[:e.held]))
		e.held = 0
	}
	return nil
}

// Output returns the encoded text after Close.
func (e *Encoder) Output() []byte { return e.out }
