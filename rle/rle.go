// Package rle provides escaped run-length encoding over Unicode code points.
// Decoding is strict: every accepted t satisfies Encode(Decode(t)) == t.
package rle

import (
	"errors"
	"unicode/utf8"

	"ontology/runs"
)

var (
	ErrExplicitOne      = errors.New("rle: explicit count of 1")
	ErrCountZero        = errors.New("rle: count is zero")
	ErrLeadingZero      = errors.New("rle: count has leading zero")
	ErrAdjacentRuns     = errors.New("rle: adjacent runs share a symbol")
	ErrBadEscape        = errors.New("rle: backslash must escape a digit or backslash")
	ErrTrailingSlash    = errors.New("rle: trailing backslash")
	ErrMissingSymbol    = errors.New("rle: count without a symbol")
	ErrInvalidUTF8      = errors.New("rle: invalid UTF-8 in symbol")
	ErrCountOverflow    = errors.New("rle: count overflows uint64")
	ErrOutputTooLarge   = errors.New("rle: decoded output exceeds limit")
	ErrDecoderFinished  = errors.New("rle: decoder already closed")
)

// SyntaxError is a decoding failure classified by Kind (one of the sentinels
// above) at absolute byte Offset in the input.
type SyntaxError struct {
	Kind   error
	Offset int
}

func (e *SyntaxError) Error() string { return e.Kind.Error() }
func (e *SyntaxError) Unwrap() error { return e.Kind }

// DefaultMaxBytes caps decoded output unless Decoder.MaxBytes is set.
const DefaultMaxBytes = 1 << 30

// Encode returns the canonical RLE form of s.
func Encode(s string) string {
	var out []byte
	for _, run := range runs.Longest(s) {
		if run.Count != 1 {
			out = runs.AppendCount(out, run.Count)
		}
		if run.Sym == '\\' || (run.Sym >= '0' && run.Sym <= '9') {
			out = append(out, '\\')
		}
		out = utf8.AppendRune(out, run.Sym)
	}
	return string(out)
}

// Decode strictly decodes t. It is equivalent to streaming the whole string.
func Decode(t string) (string, error) {
	d := NewDecoder()
	if _, err := d.Write([]byte(t)); err != nil {
		return "", err
	}
	if err := d.Close(); err != nil {
		return "", err
	}
	return d.String(), nil
}

// Decoder is a streaming strict decoder.
type Decoder struct {
	MaxBytes int

	out        []byte
	checked    uint64
	buf        []byte
	base       int
	count      uint64
	digits     int
	countStart int
	prevSym    rune
	havePrev   bool
	fatal      error
	finished   bool
}

// NewDecoder returns a decoder with the default output limit.
func NewDecoder() *Decoder { return &Decoder{MaxBytes: DefaultMaxBytes} }

// String returns the decoded output accumulated so far.
func (d *Decoder) String() string { return string(d.out) }

// BytesChecked reports how many input bytes have been fed to Write.
func (d *Decoder) BytesChecked() uint64 { return d.checked }

// Write feeds a chunk of encoded input.
func (d *Decoder) Write(p []byte) (int, error) {
	d.checked += uint64(len(p))
	if d.fatal != nil {
		return 0, d.fatal
	}
	if d.finished {
		return 0, &SyntaxError{Kind: ErrDecoderFinished, Offset: d.base}
	}
	d.buf = append(d.buf, p...)
	if err := d.parse(false); err != nil {
		d.fatal = err
		return 0, err
	}
	return len(p), nil
}

// Close signals end of input and validates trailing state.
func (d *Decoder) Close() error {
	if d.fatal != nil {
		return d.fatal
	}
	if d.finished {
		return ErrDecoderFinished
	}
	if err := d.parse(true); err != nil {
		d.fatal = err
		return err
	}
	d.finished = true
	return nil
}

func (d *Decoder) fail(kind error, offset int) error {
	return &SyntaxError{Kind: kind, Offset: offset}
}

func (d *Decoder) parse(atEnd bool) error {
	i := 0
	for i < len(d.buf) {
		b := d.buf[i]
		switch {
		case b >= '0' && b <= '9':
			if d.digits == 0 {
				d.countStart = d.base + i
				d.count = uint64(b - '0')
				d.digits = 1
				i++
				continue
			}
			if d.count == 0 {
				return d.fail(ErrLeadingZero, d.base+i)
			}
			next, ok := runs.AddDigit(d.count, b)
			if !ok {
				return d.fail(ErrCountOverflow, d.base+i)
			}
			d.count = next
			d.digits++
			i++
		case b == '\\':
			if i+1 >= len(d.buf) {
				if atEnd {
					return d.fail(ErrTrailingSlash, d.base+i)
				}
				d.keep(i)
				return nil
			}
			c := d.buf[i+1]
			if !((c >= '0' && c <= '9') || c == '\\') {
				return d.fail(ErrBadEscape, d.base+i)
			}
			if err := d.complete(rune(c), 1, d.base+i); err != nil {
				return err
			}
			i += 2
		default:
			if !utf8.FullRune(d.buf[i:]) {
				if atEnd {
					return d.fail(ErrInvalidUTF8, d.base+i)
				}
				d.keep(i)
				return nil
			}
			r, size := utf8.DecodeRune(d.buf[i:])
			if r == utf8.RuneError && size == 1 {
				return d.fail(ErrInvalidUTF8, d.base+i)
			}
			if err := d.complete(r, size, d.base+i); err != nil {
				return err
			}
			i += size
		}
	}
	if atEnd && d.digits > 0 {
		return d.fail(ErrMissingSymbol, d.countStart)
	}
	d.base += i
	d.buf = d.buf[:0]
	return nil
}

func (d *Decoder) keep(i int) {
	d.base += i
	d.buf = append(d.buf[:0], d.buf[i:]...)
}

func (d *Decoder) complete(sym rune, outLen, symOffset int) error {
	n := uint64(1)
	if d.digits > 0 {
		n = d.count
		if n == 1 {
			return d.fail(ErrExplicitOne, symOffset)
		}
		if n == 0 {
			return d.fail(ErrCountZero, symOffset)
		}
	}
	if d.havePrev && d.prevSym == sym {
		return d.fail(ErrAdjacentRuns, symOffset)
	}
	remaining := d.MaxBytes - len(d.out)
	if d.MaxBytes <= 0 || remaining < 0 || !runs.Fits(n, outLen, remaining) {
		return d.fail(ErrOutputTooLarge, symOffset)
	}
	for k := uint64(0); k < n; k++ {
		d.out = utf8.AppendRune(d.out, sym)
	}
	d.prevSym, d.havePrev, d.digits = sym, true, 0
	d.count = 0
	return nil
}
