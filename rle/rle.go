// Package rle implements an escaped run-length codec. A run is an
// optional decimal count plus one symbol rune; a digit or '\' symbol
// is written as '\x'. A count of 1 is omitted. Decode is strict: it
// accepts only canonical encodings, so Encode(Decode(t)) == t holds.
package rle

import (
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"

	"ontology/runs"
)

// Kind classifies a decode error; the Err* constants are sentinels
// matchable with errors.Is.
type Kind int

const (
	ErrExplicitOne       Kind = iota + 1 // count written as "1"
	ErrCountZero                         // count written as "0"
	ErrLeadingZero                       // count with a leading zero
	ErrAdjacentSame                      // adjacent runs share a symbol
	ErrBadEscape                         // '\' followed by neither digit nor '\'
	ErrTrailingBackslash                 // input ends with a lone '\'
	ErrMissingSymbol                     // input ends with a count but no symbol
	ErrInvalidUTF8                       // input is not valid UTF-8
	ErrTooLarge                          // decoded output exceeds the limit
)

var text = map[Kind]string{
	ErrExplicitOne: "explicit count 1", ErrCountZero: "count 0", ErrLeadingZero: "leading zero",
	ErrAdjacentSame: "adjacent equal symbols", ErrBadEscape: "bad escape", ErrInvalidUTF8: "invalid UTF-8",
	ErrTrailingBackslash: "trailing backslash", ErrMissingSymbol: "count without symbol", ErrTooLarge: "output over limit",
}

func (k Kind) Error() string { return "rle: " + text[k] }

// Error is the only error type this package returns.
type Error struct {
	Kind   Kind
	Offset int64
}

func (e *Error) Error() string {
	return e.Kind.Error() + " at byte " + strconv.FormatInt(e.Offset, 10)
}

// Is matches any Err* sentinel of the same kind.
func (e *Error) Is(t error) bool { k, ok := t.(Kind); return ok && k == e.Kind }

// DefaultLimit bounds the output size of Decode.
var DefaultLimit int64 = 1 << 30

var inspected int64 // input bytes examined so far (asserted by tests)

// Encode returns the canonical encoding of s: longest runs, no Unicode
// normalization (é and e + U+0301 stay distinct).
func Encode(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for sym, n := range runs.All(s) {
		if n > 1 {
			b.Write(runs.AppendDecimal(nil, n))
		}
		if sym == '\\' || sym >= '0' && sym <= '9' {
			b.WriteByte('\\')
		}
		b.WriteRune(sym)
	}
	return b.String()
}

// Decode strictly decodes t, bounded by DefaultLimit.
func Decode(t string) (string, error) {
	d := NewDecoder(DefaultLimit)
	if _, err := d.Write([]byte(t)); err != nil {
		return "", err
	}
	if err := d.Close(); err != nil {
		return "", err
	}
	return d.String(), nil
}

// Decoder decodes incrementally; input may be split into any chunks.
type Decoder struct {
	out                   strings.Builder
	digits                string
	buf                   [utf8.UTFMax]byte
	last                  rune
	limit, pos, symAt     int64
	countAt, escAt, bufAt int64
	blen, bneed           int
	escaped, hasSym       bool
	err                   error
}

func NewDecoder(limit int64) *Decoder { return &Decoder{limit: limit} }

// String returns everything decoded so far.
func (d *Decoder) String() string { return d.out.String() }

func (d *Decoder) Write(p []byte) (int, error) {
	for i, c := range p {
		if d.err != nil {
			return i, d.err
		}
		d.step(c)
		d.pos++
	}
	return len(p), d.err
}

func (d *Decoder) Close() error {
	switch {
	case d.err != nil:
	case d.bneed > 0:
		d.fail(ErrInvalidUTF8, d.bufAt)
	case d.escaped:
		d.fail(ErrTrailingBackslash, d.escAt)
	case d.digits != "":
		d.fail(ErrMissingSymbol, d.countAt)
	}
	return d.err
}

func (d *Decoder) fail(k Kind, off int64) {
	if d.err == nil {
		d.err = &Error{Kind: k, Offset: off}
	}
}

func (d *Decoder) step(c byte) {
	inspected++
	switch {
	case d.bneed > 0: // continuation byte of a multi-byte rune
		d.buf[d.blen] = c
		d.blen++
		if d.blen < d.bneed {
			return
		}
		r, sz := utf8.DecodeRune(d.buf[:d.blen])
		d.blen, d.bneed = 0, 0
		if r == utf8.RuneError && sz == 1 {
			d.fail(ErrInvalidUTF8, d.bufAt)
			return
		}
		d.emit(r)
	case d.escaped:
		d.escaped = false
		if c == '\\' || c >= '0' && c <= '9' {
			d.emit(rune(c))
		} else {
			d.fail(ErrBadEscape, d.pos)
		}
	case c == '\\':
		d.escaped, d.escAt, d.symAt = true, d.pos, d.pos
	case c >= '0' && c <= '9':
		if d.digits == "" {
			d.countAt = d.pos
		}
		d.digits += string(c)
	case c < utf8.RuneSelf:
		d.symAt = d.pos
		d.emit(rune(c))
	default:
		if c < 0xC2 || c > 0xF4 {
			d.fail(ErrInvalidUTF8, d.pos)
			return
		}
		d.buf[0], d.blen, d.bneed = c, 1, [4]int{2, 2, 3, 4}[c>>4-12]
		d.bufAt, d.symAt = d.pos, d.pos
	}
}

func (d *Decoder) emit(sym rune) {
	n, runAt := big.NewInt(1), d.symAt
	if d.digits != "" {
		runAt = d.countAt
		switch {
		case d.digits == "0":
			d.fail(ErrCountZero, runAt)
		case d.digits[0] == '0':
			d.fail(ErrLeadingZero, runAt)
		case d.digits == "1":
			d.fail(ErrExplicitOne, runAt)
		default:
			n, _ = runs.ParseDecimal(d.digits)
		}
		d.digits = ""
		if d.err != nil {
			return
		}
	}
	if d.hasSym && d.last == sym {
		d.fail(ErrAdjacentSame, runAt)
		return
	}
	d.last, d.hasSym = sym, true
	sz := utf8.RuneLen(sym)
	need := new(big.Int).Mul(n, big.NewInt(int64(sz)))
	if !need.IsInt64() || int64(d.out.Len())+need.Int64() > d.limit {
		d.fail(ErrTooLarge, runAt)
		return
	}
	var b [utf8.UTFMax]byte
	utf8.EncodeRune(b[:], sym)
	for i := n.Int64(); i > 0; i-- {
		d.out.Write(b[:sz])
	}
}
