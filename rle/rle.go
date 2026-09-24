// Package rle implements escaped run-length encoding on top of package runs.
package rle

import (
	"errors"
	"unicode/utf8"

	"ontology/runs"
)

// Rejection reasons. Each is distinguishable via errors.Is.
var (
	ErrExplicitOne      = errors.New("rle: explicit count 1")
	ErrZeroCount        = errors.New("rle: count 0")
	ErrLeadingZero      = errors.New("rle: leading zero in count")
	ErrAdjacent         = errors.New("rle: adjacent runs with same symbol")
	ErrBadEscape        = errors.New("rle: backslash followed by non-digit/non-backslash")
	ErrTrailingBackslash = errors.New("rle: trailing backslash")
	ErrTrailingCount    = errors.New("rle: count without following symbol")
	ErrInvalidUTF8      = errors.New("rle: invalid UTF-8 in symbol")
	ErrOutputLimit      = errors.New("rle: decoded output exceeds limit")
	ErrClosed           = errors.New("rle: decoder already closed")
	// ErrCountTooLarge is re-exported from package runs.
	ErrCountTooLarge = runs.ErrCountTooLarge
)

// DecodeError pairs a rejection reason with a byte offset in the input.
type DecodeError struct {
	Offset int
	Err    error
}

func (e *DecodeError) Error() string { return e.Err.Error() }
func (e *DecodeError) Unwrap() error { return e.Err }

const (
	stStart = iota // expecting the start of a run
	stCount        // reading decimal digits
	stEsc          // saw '\', one byte must follow
	stUTF8         // inside a multi-byte symbol
)

// Decoder is a streaming strict decoder. Every input byte is examined once;
// BytesChecked reports the running total.
type Decoder struct {
	out        []byte
	state      int
	digits     []byte
	runStart   int
	utf        [utf8.UTFMax]byte
	utfLeft    int
	lastSym    rune
	haveLast   bool
	err        error
	closed     bool
	offset     int
	checked    int
	maxOut     int
}

// Option configures a Decoder.
type Option func(*Decoder)

// WithMaxOutput caps the total decoded output length.
func WithMaxOutput(n int) Option { return func(d *Decoder) { d.maxOut = n } }

// NewDecoder builds a streaming decoder (default output limit: 64 MiB).
func NewDecoder(opts ...Option) *Decoder {
	d := &Decoder{maxOut: 64 << 20}
	for _, o := range opts {
		o(d)
}
	return d
}

// BytesChecked returns how many input bytes have been examined so far.
func (d *Decoder) BytesChecked() int { return d.checked }

func (d *Decoder) fail(off int, err error) error {
	d.err = &DecodeError{Offset: off, Err: err}
	return d.err
}

func (d *Decoder) checkCount() (uint64, error) {
	if len(d.digits) == 0 {
		return 1, nil
	}
	s := string(d.digits)
	if s[0] == '0' {
		if s == "0" {
			return 0, d.fail(d.runStart, ErrZeroCount)
		}
		return 0, d.fail(d.runStart, ErrLeadingZero)
	}
	n, err := runs.ParseCount(s)
	if err != nil {
		return 0, d.fail(d.runStart, err)
	}
	if n == 1 {
		return 0, d.fail(d.runStart, ErrExplicitOne)
	}
	return n, nil
}

func (d *Decoder) emit(sym rune) error {
	n, err := d.checkCount()
	if err != nil {
		return err
	}
	if d.haveLast && sym == d.lastSym {
		return d.fail(d.runStart, ErrAdjacent)
	}
	if d.maxOut >= 0 && uint64(len(d.out))+n > uint64(d.maxOut) {
		return d.fail(d.runStart, ErrOutputLimit)
	}
	d.digits = d.digits[:0]
	d.state = stStart
	var buf [utf8.UTFMax]byte
	w := utf8.EncodeRune(buf[:], sym)
	for i := uint64(0); i < n; i++ {
		d.out = append(d.out, buf[:w]...)
	}
	d.lastSym, d.haveLast = sym, true
	return nil
}

func (d *Decoder) startSymbol(b byte, off int) error {
	switch {
	case b < 0x80:
		return d.emit(rune(b))
	case b&0xE0 == 0xC0:
		d.utfLeft, d.utf[0] = 1, b
	case b&0xF0 == 0xE0:
		d.utfLeft, d.utf[0] = 2, b
	case b&0xF8 == 0xF0:
		d.utfLeft, d.utf[0] = 3, b
	default:
		return d.fail(off, ErrInvalidUTF8)
	}
	d.state = stUTF8
	return nil
}

func (d *Decoder) feed(b byte) error {
	off := d.offset
	d.offset++
	d.checked++
	switch d.state {
	case stStart:
		if b >= '0' && b <= '9' {
			d.runStart, d.state = off, stCount
			d.digits = append(d.digits[:0], b)
			return nil
		}
		if b == '\\' {
			d.runStart, d.state = off, stEsc
			return nil
		}
		return d.startSymbol(b, off)
	case stCount:
		if b >= '0' && b <= '9' {
			d.digits = append(d.digits, b)
			return nil
		}
		if b == '\\' {
			d.state = stEsc
			return nil
		}
		return d.startSymbol(b, off)
	case stEsc:
		if (b >= '0' && b <= '9') || b == '\\' {
			return d.emit(rune(b))
		}
		return d.fail(off, ErrBadEscape)
	default: // stUTF8
		if b&0xC0 != 0x80 {
			return d.fail(off, ErrInvalidUTF8)
		}
		pos := utf8.UTFMax - d.utfLeft
		d.utf[pos] = b
		d.utfLeft--
		if d.utfLeft > 0 {
			return nil
		}
		r, _ := utf8.DecodeRune(d.utf[pos-1+1-utf8.UTFMax:])
		_ = pos
		return d.emit(r)
	}
}

// Write feeds one chunk. Byte boundaries may split counts, escapes or UTF-8.
func (d *Decoder) Write(p []byte) (int, error) {
	if d.closed {
		return 0, ErrClosed
	}
	if d.err != nil {
		return 0, d.err
	}
	for _, b := range p {
		if err := d.feed(b); err != nil {
			return len(p), err
		}
	}
	return len(p), nil
}

// Close validates the tail and releases no resources.
func (d *Decoder) Close() error {
	if d.closed {
		if d.err == nil {
			return ErrClosed
		}
		return d.err
	}
	d.closed = true
	switch d.state {
	case stEsc:
		return d.fail(d.offset, ErrTrailingBackslash)
	case stCount:
		return d.fail(d.runStart, ErrTrailingCount)
	case stUTF8:
		return d.fail(d.offset, ErrInvalidUTF8)
}
	return d.err
}

// String returns the decoded output accumulated so far.
func (d *Decoder) String() string { return string(d.out) }

// Decode strictly decodes t with the given options.
func Decode(t string, opts ...Option) (string, error) {
	d := NewDecoder(opts...)
	if _, err := d.Write([]byte(t)); err != nil {
		return "", err
	}
	if err := d.Close(); err != nil {
		return "", err
	}
	return d.String(), nil
}

// Encode produces the canonical escaped RLE form of s.
func Encode(s string) string {
	var out []byte
	runs.Split(s, func(r runs.Run) {
		if r.Count >= 2 {
			out = runs.AppendCount(out, r.Count)
		}
		if r.Sym >= '0' && r.Sym <= '9' || r.Sym == '\\' {
			out = append(out, '\\')
		}
		var buf [utf8.UTFMax]byte
		w := utf8.EncodeRune(buf[:], r.Sym)
		out = append(out, buf[:w]...)
	})
	return string(out)
}
