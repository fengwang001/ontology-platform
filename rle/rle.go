// Package rle implements a strict canonical run-length encoding with
// backslash escaping over Unicode code points.
package rle

import (
	"errors"
	"io"
	"strings"
	"unicode/utf8"

	"ontology/runs"
)

// Sentinel errors mark every malformed-input category (errors.Is). OffsetError
// (errors.As) attaches the zero-based byte offset.
var (
	ErrCountOne      = errors.New("rle: explicit count of 1 is non-canonical")
	ErrCountZero     = errors.New("rle: count of 0 is not encodable")
	ErrLeadingZero   = errors.New("rle: count has a leading zero")
	ErrAdjacentSame  = errors.New("rle: adjacent runs with the same symbol")
	ErrBadEscape     = errors.New("rle: backslash must escape a digit or backslash")
	ErrTrailingSlash = errors.New("rle: trailing backslash")
	ErrMissingSymbol = errors.New("rle: count without a following symbol")
	ErrInvalidUTF8   = errors.New("rle: invalid UTF-8 in input")
	ErrCountTooLarge = errors.New("rle: count exceeds MaxInt64")
	ErrOutputLimit   = errors.New("rle: decoded output exceeds limit")
)

// OffsetError attaches a zero-based byte offset to a decoding error.
type OffsetError struct {
	Offset int
	Err    error
}

func (e *OffsetError) Error() string { return e.Err.Error() }
func (e *OffsetError) Unwrap() error { return e.Err }

func escaped(r rune) bool { return r == '\\' || r >= '0' && r <= '9' }

// Encode returns the canonical RLE form of s.
func Encode(s string) string {
	rs := runs.Split(s)
	out := make([]byte, 0, len(s)+len(rs))
	for _, r := range rs {
		out = runs.AppendCount(out, r.Count)
		if escaped(r.Symbol) {
			out = append(out, '\\')
		}
		out = utf8.AppendRune(out, r.Symbol)
	}
	return string(out)
}

// DefaultOutputLimit caps decoded output for Decode (64 MiB).
const DefaultOutputLimit = 64 << 20

// Decode strictly decodes t, rejecting every non-canonical form.
func Decode(t string) (string, error) {
	var sb strings.Builder
	w := NewWriter(&sb, DefaultOutputLimit)
	if _, err := w.Write([]byte(t)); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	return sb.String(), nil
}

// DecodeLimit is Decode with a custom decoded-byte budget (<=0 unlimited).
func DecodeLimit(t string, limit int64) (string, error) {
	var sb strings.Builder
	w := NewWriter(&sb, limit)
	if _, err := w.Write([]byte(t)); err != nil {
		return "", err
	}
	return sb.String(), w.Close()
}

// Writer is a streaming strict decoder: a byte state machine examining each
// input byte exactly once, so arbitrary chunking yields identical results.
type Writer struct {
	dst      io.Writer
	limit    int64
	written  int64
	offset   int
	digits   []byte
	esc      bool
	cont     int
	seen     int
	mb       [utf8.UTFMax]byte
	prev     rune
	havePrev bool
	examined int64
}

// NewWriter streams canonical RLE decoding to dst; limit<=0 disables the cap.
func NewWriter(dst io.Writer, limit int64) *Writer { return &Writer{dst: dst, limit: limit} }

// Examined reports the number of input bytes examined so far.
func (w *Writer) Examined() int64 { return w.examined }

func (w *Writer) oe(pos int, err error) error { return &OffsetError{Offset: pos, Err: err} }

// Write feeds one chunk. On error the Writer is unusable; offsets are global.
func (w *Writer) Write(p []byte) (int, error) {
	for _, b := range p {
		pos := w.offset
		w.examined++
		w.offset++
		if err := w.feed(b, pos); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

// Close finalizes the stream, rejecting dangling counts or escapes.
func (w *Writer) Close() error {
	switch {
	case w.esc:
		return w.oe(w.offset-1, ErrTrailingSlash)
	case w.cont > 0:
		return w.oe(w.offset-w.seen-1, ErrInvalidUTF8)
	case len(w.digits) > 0:
		return w.oe(w.offset-len(w.digits), ErrMissingSymbol)
	}
	return nil
}

func (w *Writer) feed(b byte, pos int) error {
	if w.cont > 0 {
		return w.feedCont(b, pos)
	}
	switch {
	case w.esc:
		w.esc = false
		if b != '\\' && (b < '0' || b > '9') {
			return w.oe(pos-1, ErrBadEscape)
		}
		return w.emit(rune(b), 1, pos)
	case b >= '0' && b <= '9':
		return w.digit(b, pos)
	case b == '\\':
		w.esc = true
		return nil
	case b < 0x80:
		return w.emit(rune(b), 1, pos)
	case b >= 0xC2 && b <= 0xF4:
		w.cont = int(firstByteLen[b]) - 1
		w.seen = 0
		w.mb[0] = b
		return nil
	default:
		return w.oe(pos, ErrInvalidUTF8)
	}
}

var firstByteLen = [256]byte{0xC2: 2, 0xE0: 3}

func init() {
	for b := 0xC2; b <= 0xDF; b++ {
		firstByteLen[b] = 2
	}
	for b := 0xE0; b <= 0xEF; b++ {
		firstByteLen[b] = 3
	}
	for b := 0xF0; b <= 0xF4; b++ {
		firstByteLen[b] = 4
	}
}

func (w *Writer) feedCont(b byte, pos int) error {
	k := w.seen + 1
	w.mb[k] = b
	w.seen++
	w.cont--
	if b < 0x80 || b > 0xBF {
		return w.oe(pos, ErrInvalidUTF8)
	}
	if w.cont > 0 {
		return nil
	}
	r, _ := utf8.DecodeRune(w.mb[:w.seen+1])
	start := pos - w.seen
	if r == utf8.RuneError {
		return w.oe(start, ErrInvalidUTF8)
	}
	return w.emit(r, w.seen+1, start)
}

func (w *Writer) digit(b byte, pos int) error {
	d := w.digits
	start := pos - len(d)
	if len(d) == 0 {
		w.digits = append(w.digits[:0], b)
		return nil
	}
	if d[0] == '0' {
		return w.oe(pos, ErrLeadingZero)
	}
	if len(d) >= 18 {
		if _, err := runs.ParseCount(string(append(d, b))); err != nil {
			return w.oe(pos, mapCountErr(err))
		}
	}
	w.digits = append(d, b)
	return nil
}

func mapCountErr(err error) error {
	switch {
	case errors.Is(err, runs.ErrCountTooLarge):
		return ErrCountTooLarge
	case errors.Is(err, runs.ErrLeadingZero):
		return ErrLeadingZero
	case errors.Is(err, runs.ErrCountZero):
		return ErrCountZero
	default:
		return err
	}
}

func (w *Writer) emit(r rune, width, pos int) error {
	if w.havePrev && r == w.prev {
		return w.oe(pos, ErrAdjacentSame)
	}
	n := int64(1)
	if len(w.digits) > 0 {
		c, err := runs.ParseCount(string(w.digits))
		if err != nil {
			return w.oe(pos-len(w.digits), mapCountErr(err))
		}
		n = c
		w.digits = w.digits[:0]
		if n == 1 {
			return w.oe(pos-len(string(append([]byte(nil), '1'))), ErrCountOne)
		}
	}
	if w.limit > 0 {
		add := n * int64(width)
		if add/int64(width) != n || w.written+add > w.limit {
			return w.oe(pos, ErrOutputLimit)
		}
	}
	var rep [utf8.UTFMax]byte
	m := utf8.EncodeRune(rep[:], r)
	chunk := rep[:m]
	for i := int64(0); i < n; i++ {
		if _, err := w.dst.Write(chunk); err != nil {
			return err
		}
	}
	w.written += n * int64(width)
	w.prev, w.havePrev = r, true
	return nil
}
