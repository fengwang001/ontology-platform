package rle

import (
	"errors"
	"fmt"
	"io"
	"ontology/runs"
	"strings"
	"unicode/utf8"
)

const DefaultMaxOutputBytes = 1 << 20

var ErrExplicitOne, ErrZeroCount = errors.New("rle: explicit count of one"), errors.New("rle: count is zero")
var ErrLeadingZeroCount, ErrAdjacentSameSymbol = errors.New("rle: count has leading zero"), errors.New("rle: adjacent runs have the same symbol")
var ErrInvalidEscape, ErrTrailingBackslash = errors.New("rle: invalid escape"), errors.New("rle: trailing backslash")
var ErrCountWithoutSymbol, ErrInvalidUTF8 = errors.New("rle: count without symbol"), errors.New("rle: invalid UTF-8")
var ErrOutputLimit, ErrCountOverflow = errors.New("rle: output limit exceeded"), runs.ErrCountOverflow

type DecodeError struct {
	Offset int
	Kind   error
}

func (e *DecodeError) Error() string { return fmt.Sprintf("%s at byte %d", e.Kind, e.Offset) }
func (e *DecodeError) Unwrap() error { return e.Kind }

func Encode(s string) string {
	var b strings.Builder
	for _, run := range runs.Split(s) {
		if run.Count != 1 {
			b.WriteString(runs.FormatCount(run.Count))
		}
		if run.Symbol == '\\' || '0' <= run.Symbol && run.Symbol <= '9' {
			b.WriteByte('\\')
		}
		b.WriteRune(run.Symbol)
	}
	return b.String()
}

func Decode(t string) (string, error) {
	out := &strings.Builder{}
	d := NewDecoderLimit(out, DefaultMaxOutputBytes)
	if _, err := d.Write([]byte(t)); err != nil {
		return "", err
	}
	return out.String(), d.Close()
}

type Decoder struct {
	w                                                                    io.Writer
	limit, produced, examined, count, countStart, escapeStart, runeStart int
	err                                                                  *DecodeError
	prev                                                                 rune
	haveCount, escaped, havePrev                                         bool
	pending                                                              [4]byte
	pendingLen                                                           int
}

func NewDecoder() *Decoder { return NewDecoderLimit(io.Discard, 0) }
func NewDecoderLimit(w io.Writer, limit int) *Decoder {
	if limit <= 0 {
		limit = DefaultMaxOutputBytes
	}
	return &Decoder{w: w, limit: limit}
}

func (d *Decoder) Write(p []byte) (int, error) {
	if d.err != nil {
		return 0, d.err
	}
	for i := 0; i < len(p); {
		global, b := d.examined+i, p[i]
		errAt, err := i, error(nil)
		switch {
		case d.escaped:
			if b != '\\' && (b < '0' || b > '9') {
				err = d.fail(ErrInvalidEscape, d.escapeStart)
			} else {
				d.escaped = false
				err = d.emit(rune(b), 1, d.escapeStart)
			}
			errAt = i
		case b == '\\':
			d.escaped, d.escapeStart = true, global
		case '0' <= b && b <= '9':
			errAt = i
			if d.haveCount && d.count == 0 {
				err = d.fail(ErrLeadingZeroCount, d.countStart)
			} else {
				if !d.haveCount {
					d.haveCount, d.count, d.countStart = true, 0, global
				}
				next, addErr := runs.AddDigit(d.count, b)
				if addErr != nil {
					err = d.fail(addErr, global)
				} else {
					d.count = next
				}
			}
		case b < 0x80:
			err, errAt = d.emit(rune(b), 1, global), i
		default:
			r, size, n, start, complete := d.takeRune(p[i:], global)
			i += n - 1
			if !complete {
				break
			}
			errAt = start - d.examined
			if r == utf8.RuneError {
				err = d.fail(ErrInvalidUTF8, start)
				break
			}
			err = d.emit(r, size, start)
		}
		if err != nil {
			return errAt, err
		}
		i++
	}
	d.examined += len(p)
	return len(p), nil
}

func (d *Decoder) takeRune(rest []byte, offset int) (rune, int, int, int, bool) {
	start := offset - d.pendingLen
	for n := 1; n <= len(rest); n++ {
		d.pending[d.pendingLen] = rest[n-1]
		d.pendingLen++
		if !utf8.FullRune(d.pending[:d.pendingLen]) {
			continue
		}
		r, size := utf8.DecodeRune(d.pending[:d.pendingLen])
		d.pendingLen = 0
		return r, size, n, start, true
	}
	d.runeStart = start
	return utf8.RuneError, 1, len(rest), start, false
}

func (d *Decoder) Close() error {
	switch {
	case d.err != nil:
		return d.err
	case d.escaped:
		return d.fail(ErrTrailingBackslash, d.escapeStart)
	case d.pendingLen > 0:
		return d.fail(ErrInvalidUTF8, d.runeStart)
	case d.haveCount:
		return d.fail(ErrCountWithoutSymbol, d.countStart)
	}
	return nil
}

func (d *Decoder) emit(symbol rune, size, start int) error {
	count := 1
	if d.haveCount {
		switch d.count {
		case 0:
			return d.fail(ErrZeroCount, d.countStart)
		case 1:
			return d.fail(ErrExplicitOne, d.countStart)
		}
		count = d.count
	}
	if d.havePrev && d.prev == symbol {
		return d.fail(ErrAdjacentSameSymbol, start)
	}
	if !runs.Fits(count, size, d.limit-d.produced) {
		return d.fail(ErrOutputLimit, start)
	}
	if err := runs.WriteRepeated(d.w, symbol, count); err != nil {
		return err
	}
	d.produced += count * size
	d.haveCount, d.count = false, 0
	d.prev, d.havePrev = symbol, true
	return nil
}

func (d *Decoder) fail(kind error, offset int) error {
	d.err = &DecodeError{Offset: offset, Kind: kind}
	return d.err
}
