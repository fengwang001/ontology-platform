// Package rle implements escaped text run-length encoding and strict,
// split-independent streaming decoding.
package rle

import (
	"errors"
	"io"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"

	"ontology/runs"
)

var (
	ErrCountOne       = errors.New("rle: explicit count of 1")
	ErrCountZero      = errors.New("rle: count of 0")
	ErrLeadingZero    = errors.New("rle: count with leading zero")
	ErrAdjacentSymbol = errors.New("rle: adjacent runs with same symbol")
	ErrBadEscape      = errors.New("rle: backslash not followed by digit or backslash")
	ErrTrailingSlash  = errors.New("rle: trailing backslash")
	ErrMissingSymbol  = errors.New("rle: count without a following symbol")
	ErrInvalidUTF8    = errors.New("rle: invalid UTF-8 in symbol")
	ErrOutputLimit    = errors.New("rle: decoded output exceeds configured limit")
)

// DecodeError attaches the 0-based byte offset to a sentinel decode error.
type DecodeError struct {
	Err    error
	Offset int64
}

func (e *DecodeError) Error() string {
	return e.Err.Error() + " at byte offset " + strconv.FormatInt(e.Offset, 10)
}
func (e *DecodeError) Unwrap() error { return e.Err }

// Option configures a streaming Decoder.
type Option func(*Decoder)

// WithOutputLimit bounds total decoded output bytes; non-positive means no limit.
func WithOutputLimit(limit int64) Option {
	return func(d *Decoder) { d.limit = limit }
}

// Decoder holds back one run: same-symbol adjacency is only visible when the
// next run's symbol arrives. Retained input is always a suffix of one chunk.
type Decoder struct {
	w        io.Writer
	limit    int64
	out      int64
	seen     int64
	base     int64
	hold     []byte
	acc      runs.Accumulator
	leadZero bool
	start    int64
	havePrev bool
	prevR    rune
	prevN    *big.Int
	prevSize int64
	prevAt   int64
}

// NewDecoder creates a streaming decoder writing decoded text to w.
func NewDecoder(w io.Writer, opts ...Option) *Decoder {
	d := &Decoder{w: w, limit: 1 << 30, start: -1, prevAt: -1}
	for _, o := range opts {
		o(d)
	}
	return d
}

// BytesExamined reports how many input bytes have been examined, exactly once.
func (d *Decoder) BytesExamined() int64 { return d.seen }

func (d *Decoder) err(err error, at int64) error {
	return &DecodeError{Err: err, Offset: at}
}

func (d *Decoder) Write(p []byte) (int, error) {
	n := len(p)
	d.seen += int64(n)
	data := append(d.hold, p...)
	d.hold = nil
	i := 0
	for i < len(data) {
		at := d.base + int64(i)
		c := data[i]
		switch {
		case c >= '0' && c <= '9':
			if d.acc.Digits() == 0 {
				d.start, d.leadZero = at, c == '0'
			} else if d.leadZero {
				return n, d.err(ErrLeadingZero, d.start)
			}
			d.acc.AddDigit(c)
			i++
		case c == '\\':
			if i == len(data)-1 {
				return n, d.retain(data, i, at)
			}
			q := data[i+1]
			if q != '\\' && (q < '0' || q > '9') {
				return n, d.err(ErrBadEscape, at)
			}
			if err := d.symbol(rune(q), 2, at); err != nil {
				return n, err
			}
			i += 2
		case c < 0x80:
			if err := d.symbol(rune(c), 1, at); err != nil {
				return n, err
			}
			i++
		default:
			r, s := utf8.DecodeRune(data[i:])
			if r == utf8.RuneError {
				if i+s == len(data) && looksTruncated(data[i:]) {
					return n, d.retain(data, int64(i), at)
				}
				return n, d.err(ErrInvalidUTF8, at)
			}
			if err := d.symbol(r, int64(s), at); err != nil {
				return n, err
			}
			i += s
		}
	}
	return n, nil
}

func looksTruncated(p []byte) bool {
	for _, n := range []int{2, 3, 4} {
		if len(p) < n {
			if _, s := utf8.DecodeRune(p); s == 1 {
				return true
			}
		}
	}
	return false
}

func (d *Decoder) retain(data []byte, i, at int64) error {
	d.hold = append(d.hold, data[i:]...)
	d.base = at
	return nil
}

func (d *Decoder) symbol(r rune, size, at int64) error {
	start := d.start
	if start < 0 {
		start = at
	}
	n := big.NewInt(1)
	if v, ok := d.acc.Value(); ok {
		n.Set(v)
		if d.leadZero {
			return d.err(ErrLeadingZero, start)
		}
		if n.Sign() == 0 {
			return d.err(ErrCountZero, start)
		}
		if n.Cmp(big.NewInt(1)) == 0 {
			return d.err(ErrCountOne, start)
		}
	}
	if d.havePrev && d.prevR == r {
		return d.err(ErrAdjacentSymbol, start)
	}
	if d.havePrev {
		if err := d.emit(d.prevR, d.prevN, d.prevSize, d.prevAt); err != nil {
			return err
		}
	}
	d.havePrev, d.prevR, d.prevN, d.prevSize, d.prevAt = true, r, n, size, start
	d.acc.Reset()
	d.start = -1
	return nil
}

func (d *Decoder) emit(r rune, n *big.Int, size, at int64) error {
	total := new(big.Int).Mul(n, big.NewInt(size))
	if d.limit > 0 {
		if new(big.Int).Add(big.NewInt(d.out), total).Cmp(big.NewInt(d.limit)) > 0 {
			return d.err(ErrOutputLimit, at)
		}
	}
	one := make([]byte, size)
	utf8.EncodeRune(one, r)
	chunk := strings.Repeat(string(one), 64*1024)
	for n.Sign() > 0 {
		k := new(big.Int).Set(n)
		if k.Cmp(big.NewInt(int64(64*1024))) > 0 {
			k.SetInt64(64 * 1024)
		}
		if _, err := d.w.Write([]byte(chunk[:int(k.Int64()*size)])); err != nil {
			return err
		}
		n.Sub(n, k)
	}
	d.out += total.Int64()
	return nil
}

func (d *Decoder) Close() error {
	if len(d.hold) > 0 {
		if d.hold[0] == '\\' {
			return d.err(ErrTrailingSlash, d.base)
		}
		return d.err(ErrInvalidUTF8, d.base)
	}
	if d.acc.Digits() > 0 {
		return d.err(ErrMissingSymbol, d.start)
	}
	if !d.havePrev {
		return nil
	}
	err := d.emit(d.prevR, d.prevN, d.prevSize, d.prevAt)
	d.havePrev = false
	return err
}

// Encode returns the canonical RLE encoding of s.
func Encode(s string) string {
	var b strings.Builder
	for _, run := range runs.Split(s) {
		b.Write(runs.AppendCount(nil, run.Count))
		if run.Symbol == '\\' || run.Symbol >= '0' && run.Symbol <= '9' {
			b.WriteByte('\\')
		}
		b.WriteRune(run.Symbol)
	}
	return b.String()
}

// Decode strictly decodes t; the first invalid run yields a DecodeError.
func Decode(t string) (string, error) {
	var b strings.Builder
	d := NewDecoder(&b)
	if _, err := d.Write([]byte(t)); err != nil {
		return "", err
	}
	if err := d.Close(); err != nil {
		return "", err
	}
	return b.String(), nil
}
