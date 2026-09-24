package rle

import (
	"errors"
	"fmt"
	"io"
	"math/big"
	"strings"
	"sync/atomic"
	"unicode/utf8"

	"ontology/runs"
)

const DefaultLimit = 1 << 26

var (
	ErrCountOne       = errors.New("rle: explicit count 1")
	ErrCountZero      = errors.New("rle: zero count")
	ErrLeadingZero    = errors.New("rle: count with leading zero")
	ErrAdjacentSame   = errors.New("rle: adjacent runs with the same symbol")
	ErrBadEscape      = errors.New("rle: escape not followed by digit or backslash")
	ErrTrailingEscape = errors.New("rle: trailing backslash")
	ErrMissingSymbol  = errors.New("rle: count without following symbol")
	ErrInvalidUTF8    = errors.New("rle: invalid UTF-8")
	ErrOutputLimit    = errors.New("rle: decoded output exceeds limit")
)

type Error struct {
	Err error
	Off int
}

func (e *Error) Error() string { return fmt.Sprintf("%v at byte offset %d", e.Err, e.Off) }
func (e *Error) Unwrap() error { return e.Err }

var checked atomic.Int64

func CheckedBytes() int64 { return checked.Load() }

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

func Encode(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for sym, n := range runs.Split(s) {
		if n > 1 {
			runs.WriteCount(&b, n)
		}
		if sym == '\\' || (sym >= '0' && sym <= '9') {
			b.WriteByte('\\')
		}
		b.WriteRune(sym)
	}
	return b.String()
}

func Decode(t string) (string, error) { return DecodeLimit(t, DefaultLimit) }

func DecodeLimit(t string, limit int64) (string, error) {
	var out strings.Builder
	var prev rune
	hasPrev := false
	for i := 0; i < len(t); {
		start, n := i, (*big.Int)(nil)
		if isDigit(t[i]) {
			v, j := runs.ReadCount(t, i)
			d := t[i:j]
			checked.Add(int64(j - i))
			i = j
			switch {
			case d == "0":
				return "", &Error{ErrCountZero, start}
			case d[0] == '0':
				return "", &Error{ErrLeadingZero, start}
			case d == "1":
				return "", &Error{ErrCountOne, start}
			}
			n = v
			if i >= len(t) {
				return "", &Error{ErrMissingSymbol, i}
			}
		}
		var sym rune
		var dec string
		if t[i] == '\\' {
			if i+1 >= len(t) {
				checked.Add(1)
				return "", &Error{ErrTrailingEscape, i}
			}
			c := t[i+1]
			if c != '\\' && !isDigit(c) {
				checked.Add(2)
				return "", &Error{ErrBadEscape, i}
			}
			sym, dec, i = rune(c), t[i+1:i+2], i+2
			checked.Add(2)
		} else {
			r, size := utf8.DecodeRuneInString(t[i:])
			if r == utf8.RuneError && size <= 1 {
				checked.Add(1)
				return "", &Error{ErrInvalidUTF8, i}
			}
			sym, dec, i = r, t[i:i+size], i+size
			checked.Add(int64(size))
		}
		if hasPrev && sym == prev {
			return "", &Error{ErrAdjacentSame, start}
		}
		prev, hasPrev = sym, true
		count, err := bounded(n, int64(len(dec)), limit-int64(out.Len()), start)
		if err != nil {
			return "", err
		}
		out.WriteString(strings.Repeat(dec, int(count)))
	}
	return out.String(), nil
}

func bounded(n *big.Int, symLen, room int64, off int) (int64, error) {
	if n == nil {
		if symLen > room {
			return 0, &Error{ErrOutputLimit, off}
		}
		return 1, nil
	}
	if !n.IsInt64() || n.Int64() > room/symLen {
		return 0, &Error{ErrOutputLimit, off}
	}
	return n.Int64(), nil
}

type Decoder struct {
	w     io.Writer
	limit int64
	buf   []byte
}

func NewDecoder(w io.Writer, limit int64) *Decoder { return &Decoder{w: w, limit: limit} }

func (d *Decoder) Write(p []byte) (int, error) { d.buf = append(d.buf, p...); return len(p), nil }

func (d *Decoder) Close() error {
	s, err := DecodeLimit(string(d.buf), d.limit)
	if err != nil {
		return err
	}
	_, err = io.WriteString(d.w, s)
	return err
}
