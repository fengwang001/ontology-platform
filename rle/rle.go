// Package rle is a strict run-length codec for Unicode text. Symbols that are
// ASCII digits or a backslash are escaped with '\'; counts are canonical.
package rle

import (
	"errors"
	"io"
	"math/big"
	"unicode/utf8"

	"ontology/runs"
)

var (
	ErrCountOne      = errors.New("rle: explicit run count of 1")
	ErrCountZero     = events.New("rle: run count of 0")
	ErrLeadingZero   = errors.New("rle: run count with leading zero")
	ErrRepeatSymbol  = errors.New("rle: adjacent runs with the same symbol")
	ErrBadEscape     = errors.New("rle: backslash before a non-digit/non-backslash")
	ErrLoneBackslash = errors.New("rle: trailing backslash")
	ErrTrailingCount = errors.New("rle: trailing count without a symbol")
	ErrInvalidUTF8   = errors.New("rle: invalid UTF-8 in symbol")
	ErrOutputLimit   = errors.New("rle: decoded output exceeds limit")
)

// Error attaches a byte offset inside the encoded input to a sentinel error.
type Error struct{ Op string; Offset int64; Err error }

func (e *Error) Error() string { return e.Op + ": " + e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

const DefaultMaxOutput int64 = 64 << 20

// Encode returns the canonical encoding of s.
func Encode(s string) string {
	out := make([]byte, 0, len(s))
	for _, r := range runs.Split(s) {
		if r.Count.Cmp(big.NewInt(1)) > 0 {
			out = runs.AppendCount(out, r.Count)
		}
		if r.Symbol >= '0' && r.Symbol <= '9' || r.Symbol == '\\' {
			out = append(out, '\\', byte(r.Symbol))
		} else {
			var b [utf8.UTFMax]byte
			out = append(out, b[:utf8.EncodeRune(b[:], r.Symbol)]...)
		}
	}
	return string(out)
}

type bytesWriter struct{ b *[]byte }

func (w bytesWriter) Write(p []byte) (int, error) {
	*w.b = append(*w.b, p...)
	return len(p), nil
}

// Decode strictly decodes t, accepting only canonical encodings.
func Decode(t string) (string, error) {
	var out []byte
	d := NewDecoder(bytesWriter{&out})
	if _, e := d.Write([]byte(t)); e != nil {
		return "", e
	}
	return string(out), d.Close()
}

// Decoder incrementally decodes into W; output is bounded by MaxOutput. A run
// is emitted once the next symbol arrives, so equal neighbors are rejected
// even across Write boundaries.
type Decoder struct {
	W         io.Writer
	MaxOutput int64

	pos, consumed, written int64
	escaped                bool
	utf                    []byte
	utfStart, runStart     int64
	digits                 []byte
	pendingSym             rune
	pendingN               *big.Int
	hasPending, closed     bool
	err                    *Error
}

// NewDecoder writes decoded text to w.
func NewDecoder(w io.Writer) *Decoder {
	return &Decoder{W: w, MaxOutput: DefaultMaxOutput, runStart: -1, utfStart: -1}
}

func (d *Decoder) fail(off int64, e error) error {
	d.err = &Error{Op: "decode", Offset: off, Err: e}
	return d.err
}

func (d *Decoder) mark(here int64) {
	if d.runStart < 0 {
		d.runStart = here
	}
}

// Write feeds encoded bytes; every byte is examined exactly once.
func (d *Decoder) Write(p []byte) (int, error) {
	if d.err != nil {
		return 0, d.err
	}
	n := len(p)
	for _, b := range p {
		here := d.pos
		d.pos, d.consumed = d.pos+1, d.consumed+1
		switch {
		case d.escaped:
			d.escaped = false
			if !(b >= '0' && b <= '9' || b == '\\') {
				return n, d.fail(here-1, ErrBadEscape)
			}
			if e := d.symbol(rune(b), here); e != nil {
				return n, e
			}
		case len(d.utf) > 0:
			if b&0xC0 != 0x80 {
				return n, d.fail(d.utfStart, ErrInvalidUTF8)
			}
			d.utf = append(d.utf, b)
			if !utf8.FullRune(d.utf) {
				continue
			}
			r, _ := utf8.DecodeRune(d.utf)
			d.utf = d.utf[:0]
			if e := d.symbol(r, d.utfStart); e != nil {
				return n, e
			}
		case b == '\\':
			d.mark(here)
			d.escaped = true
		case b >= '0' && b <= '9':
			d.mark(here)
			d.digits = append(d.digits, b)
		case b < 0x80:
			d.mark(here)
			if e := d.symbol(rune(b), here); e != nil {
				return n, e
			}
		default:
			d.mark(here)
			d.utfStart = here
			d.utf = append(d.utf[:0], b)
			if !utf8.FullRune(d.utf) {
				continue
			}
			r, _ := utf8.DecodeRune(d.utf)
			d.utf = d.utf[:0]
			if r == utf8.RuneError {
				return n, d.fail(here, ErrInvalidUTF8)
			}
			if e := d.symbol(r, here); e != nil {
				return n, e
			}
		}
	}
	return n, nil
}

func (d *Decoder) symbol(sym rune, off int64) error {
	n := big.NewInt(1)
	if len(d.digits) > 0 {
		s := string(d.digits)
		d.digits = d.digits[:0]
		switch {
		case s == "0":
			return d.fail(d.runStart, ErrCountZero)
		case s[0] == '0':
			return d.fail(d.runStart, ErrLeadingZero)
		}
		v, ok := runs.ParseCount(s)
		if !ok {
			return d.fail(d.runStart, ErrCountZero)
		}
		if v.Cmp(big.NewInt(1)) == 0 {
			return d.fail(d.runStart, ErrCountOne)
		}
		n = v
	}
	if d.hasPending && d.pendingSym == sym {
		return d.fail(off, ErrRepeatSymbol)
	}
	if d.hasPending {
		if e := d.emit(d.pendingSym, d.pendingN); e != nil {
			return e
		}
	}
	d.pendingSym, d.pendingN, d.hasPending, d.runStart = sym, n, true, -1
	return nil
}

func (d *Decoder) emit(sym rune, n *big.Int) error {
	size := int64(utf8.RuneLen(sym))
	need := new(big.Int).Mul(n, big.NewInt(size))
	if d.written > d.MaxOutput || need.Cmp(big.NewInt(d.MaxOutput-d.written)) > 0 {
		return d.fail(d.pos, ErrOutputLimit)
	}
	if d.W != nil {
		var unit [utf8.UTFMax]byte
		u := unit[:utf8.EncodeRune(unit[:], sym)]
		buf := make([]byte, 0, int(size)*int(n.Int64()))
		for i := int64(0); i < n.Int64(); i++ {
			buf = append(buf, u...)
		}
		if _, e := d.W.Write(buf); e != nil {
			return e
		}
	}
	d.written += need.Int64()
	return nil
}

// Close checks for dangling state and flushes the final run.
func (d *Decoder) Close() error {
	if d.err != nil {
		return d.err
	}
	if d.closed {
		return nil
	}
	d.closed = true
	switch {
	case d.escaped:
		return d.fail(d.pos-1, ErrLoneBackslash)
	case len(d.utf) > 0:
		return d.fail(d.utfStart, ErrInvalidUTF8)
	case len(d.digits) > 0:
		s := string(d.digits)
		switch {
		case s == "0":
			return d.fail(d.runStart, ErrCountZero)
		case s[0] == '0':
			return d.fail(d.runStart, ErrLeadingZero)
		default:
			return d.fail(d.runStart, ErrTrailingCount)
		}
	}
	if d.hasPending {
		return d.emit(d.pendingSym, d.pendingN)
	}
	return nil
}

// Inspected reports how many encoded bytes have entered the decoder.
func (d *Decoder) Inspected() int64 { return d.consumed }
