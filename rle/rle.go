// Package rle implements escaped run-length encoding with a strict decoder.
package rle

import (
	"errors"
	"fmt"
	"math/big"
	"unicode/utf8"

	"ontology/runs"
)

var (
	ErrAdjacentSame  = errors.New("adjacent runs with equal symbols")
	ErrBadEscape     = errors.New("escape not followed by digit or backslash")
	ErrLoneBackslash = errors.New("trailing backslash")
	ErrMissingSymbol = errors.New("count without symbol")
	ErrInvalidUTF8   = errors.New("invalid utf-8")
	ErrTooLarge      = errors.New("decoded output exceeds limit")
)

type Error struct {
	Kind error
	Off  int64
}

func (e *Error) Error() string { return fmt.Sprintf("rle: %s at byte %d", e.Kind, e.Off) }
func (e *Error) Unwrap() error { return e.Kind }

func Encode(s string) string {
	var b []byte
	for _, r := range runs.Split(s) {
		if r.N >= 2 {
			b = runs.AppendCount(b, r.N)
		}
		if r.Sym == '\\' || runs.IsDigit(r.Sym) {
			b = append(b, '\\')
		}
		b = utf8.AppendRune(b, r.Sym)
	}
	return string(b)
}
func Decode(t string) (string, error) {
	d := NewDecoder(1 << 30)
	d.Write([]byte(t))
	return d.Close()
}

type Decoder struct {
	out                         []byte
	err                         error
	digs, buf                   []byte
	limit, checked, dOff, bsOff int64
	last                        rune
	inCount, esc                bool
}

func NewDecoder(limit int64) *Decoder { return &Decoder{limit: limit, last: -1} }
func (d *Decoder) Checked() int64     { return d.checked }
func (d *Decoder) Write(p []byte) (int, error) {
	for _, b := range p {
		if d.err != nil {
			break
		}
		d.checked++
		if d.buf = append(d.buf, b); !utf8.FullRune(d.buf) {
			continue
		}
		r, size := utf8.DecodeRune(d.buf)
		off := d.checked - int64(len(d.buf))
		d.buf = d.buf[:0]
		if r == utf8.RuneError && size == 1 {
			d.fail(ErrInvalidUTF8, off)
		} else {
			d.sym(r, off)
		}
	}
	return len(p), d.err
}
func (d *Decoder) Close() (string, error) {
	switch {
	case d.err != nil:
		return "", d.err
	case len(d.buf) > 0:
		return "", &Error{ErrInvalidUTF8, d.checked - int64(len(d.buf))}
	case d.esc:
		return "", &Error{ErrLoneBackslash, d.bsOff}
	case d.inCount:
		return "", &Error{ErrMissingSymbol, d.dOff}
	}
	return string(d.out), nil
}
func (d *Decoder) fail(kind error, off int64) {
	if d.err == nil {
		d.err = &Error{Kind: kind, Off: off}
	}
}
func (d *Decoder) sym(r rune, off int64) {
	dig := runs.IsDigit(r)
	if d.esc {
		d.esc = false
		if !dig && r != '\\' {
			d.fail(ErrBadEscape, off)
		} else if d.inCount {
			d.finish(r)
		} else {
			d.emit(r, big.NewInt(1), d.bsOff)
		}
		return
	}
	if r == '\\' {
		d.esc, d.bsOff = true, off
		return
	}
	if d.inCount {
		if dig {
			d.digs = append(d.digs, byte(r))
		} else {
			d.finish(r)
		}
		return
	}
	if dig {
		d.inCount, d.digs, d.dOff = true, append(d.digs[:0], byte(r)), off
		return
	}
	d.emit(r, big.NewInt(1), off)
}
func (d *Decoder) finish(sym rune) {
	d.inCount = false
	if n, err := runs.ParseCount(string(d.digs)); err != nil {
		d.fail(err, d.dOff)
	} else {
		d.emit(sym, n, d.dOff)
	}
}
func (d *Decoder) emit(sym rune, n *big.Int, off int64) {
	wide := new(big.Int).Mul(n, big.NewInt(int64(utf8.RuneLen(sym))))
	switch {
	case d.last == sym:
		d.fail(ErrAdjacentSame, off)
	case wide.Cmp(big.NewInt(d.limit-int64(len(d.out)))) > 0:
		d.fail(ErrTooLarge, off)
	default:
		for i := n.Int64(); i > 0; i-- {
			d.out = utf8.AppendRune(d.out, sym)
		}
		d.last = sym
	}
}
