package rle

import (
	"errors"
	"fmt"
	"math"
	"unicode/utf8"

	"ontology/runs"
)

var (
	ErrCountOne       = errors.New("rle: explicit run count 1")
	ErrCountZero      = errors.New("rle: run count 0")
	ErrLeadingZero    = errors.New("rle: leading zero in run count")
	ErrAdjacentSymbol = errors.New("rle: adjacent runs have the same symbol")
	ErrBadEscape      = errors.New("rle: backslash must escape a digit or backslash")
	ErrTrailingEscape = errors.New("rle: trailing backslash")
	ErrTrailingCount  = errors.New("rle: trailing count without symbol")
	ErrInvalidUTF8    = errors.New("rle: invalid UTF-8")
	ErrCountTooLarge  = runs.ErrCountTooLarge
	ErrLimit          = errors.New("rle: decoded output limit exceeded")
)

type Error struct {
	Offset int
	Err    error
}

func (e *Error) Error() string { return fmt.Sprintf("%s at byte offset %d", e.Err, e.Offset) }
func (e *Error) Unwrap() error { return e.Err }

func Encode(s string) string {
	var b []byte
	for _, r := range runs.Split(s) {
		if r.Count > 1 {
			b = runs.AppendCount(b, r.Count)
		}
		if r.Symbol == '\\' || (r.Symbol >= '0' && r.Symbol <= '9') {
			b = append(b, '\\')
		}
		b = utf8.AppendRune(b, r.Symbol)
	}
	return string(b)
}

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

type Decoder struct {
	Limit       int
	out         []byte
	pending     []byte
	last        rune
	haveLast    bool
	n           int
	haveCount   bool
	leadingZero bool
	escaped     bool
	inUTF8      bool
	need        int
	runeBytes   []byte
	examined    int
	err         error
}

func NewDecoder() *Decoder { return &Decoder{Limit: math.MaxInt} }

func (d *Decoder) Write(p []byte) (int, error) {
	d.examined += len(p)
	for i, c := range p {
		if d.err == nil {
			d.feed(c, d.examined-len(p)+i)
		}
	}
	return len(p), d.err
}

func (d *Decoder) Close() error {
	if d.err == nil {
		switch {
		case d.escaped:
			d.fail(ErrTrailingEscape, d.examined)
		case d.haveCount:
			d.fail(ErrTrailingCount, d.examined)
		case d.inUTF8:
			d.fail(ErrInvalidUTF8, d.examined)
		}
	}
	return d.err
}

func (d *Decoder) String() string { return string(d.out) }
func (d *Decoder) Examined() int  { return d.examined }

func (d *Decoder) fail(err error, offset int) { d.err = &Error{Offset: offset, Err: err} }

func (d *Decoder) feed(c byte, offset int) {
	if d.inUTF8 {
		d.feedUTF8(c, offset)
		return
	}
	switch {
	case c < 0x80:
		d.feedASCII(c, offset)
	case c >= 0xC2 && c <= 0xF4:
		d.inUTF8, d.need, d.runeBytes = true, utf8Len(c)-1, append(d.runeBytes[:0], c)
	default:
		d.fail(ErrInvalidUTF8, offset)
	}
}

func (d *Decoder) feedASCII(c byte, offset int) {
	if d.escaped {
		if c == '\\' || (c >= '0' && c <= '9') {
			d.emit(rune(c), offset)
			return
		}
		d.fail(ErrBadEscape, offset)
		return
	}
	switch {
	case c == '\\':
		d.escaped = true
	case c >= '0' && c <= '9':
		d.feedDigit(c, offset)
	default:
		d.emit(rune(c), offset)
	}
}

func (d *Decoder) feedDigit(c byte, offset int) {
	if !d.haveCount {
		d.haveCount = true
		d.leadingZero = c == '0'
	} else if d.leadingZero {
		d.fail(ErrLeadingZero, offset)
		return
	}
	var err error
	if d.n, err = new(runs.CountReader).AddDigit(d.n, c); err != nil {
		d.fail(err, offset)
	}
}

func (d *Decoder) emit(symbol rune, offset int) {
	if d.escaped {
		d.escaped = false
	}
	if d.haveCount {
		switch d.n {
		case 0:
			d.fail(ErrCountZero, offset)
			return
		case 1:
			d.fail(ErrCountOne, offset)
			return
		}
	}
	if d.haveLast && d.last == symbol {
		d.fail(ErrAdjacentSymbol, offset)
		return
	}
	count := d.n
	if !d.haveCount {
		count = 1
	}
	d.n, d.haveCount, d.leadingZero = 0, false, false
	d.last, d.haveLast = symbol, true
	d.pending = utf8.AppendRune(d.pending[:0], symbol)
	if len(d.pending) > (d.limit()-len(d.out))/count {
		d.fail(ErrLimit, offset)
		return
	}
	for range count {
		d.out = append(d.out, d.pending...)
	}
}

func (d *Decoder) feedUTF8(c byte, offset int) {
	if c < 0x80 || c > 0xBF {
		d.fail(ErrInvalidUTF8, offset)
		return
	}
	d.runeBytes = append(d.runeBytes, c)
	if len(d.runeBytes) > d.need+1 {
		d.fail(ErrInvalidUTF8, offset)
		return
	}
	if len(d.runeBytes) == d.need+1 {
		symbol, _ := utf8.DecodeRune(d.runeBytes)
		d.inUTF8 = false
		d.emit(symbol, offset)
	}
}

func (d *Decoder) limit() int {
	if d.Limit <= 0 {
		return math.MaxInt
	}
	return d.Limit
}

func utf8Len(lead byte) int {
	switch {
	case lead < 0xE0:
		return 2
	case lead < 0xF0:
		return 3
	default:
		return 4
	}
}
