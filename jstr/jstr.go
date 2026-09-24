// Package jstr 实现单个 JSON 字符串字面量（含两端引号）的严格编解码。
package jstr

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"ontology/esc"
)

// 哨兵错误用 errors.Is 判别；*Error.Offset 给出 0 基字节偏移。
var (
	ErrNotString        = errors.New("jstr: not a JSON string literal")
	ErrControlChar      = errors.New("jstr: unescaped control character")
	ErrInvalidEscape    = errors.New("jstr: invalid escape sequence")
	ErrBadUnicodeEscape = errors.New("jstr: invalid \\u escape")
	ErrLoneSurrogate    = errors.New("jstr: lone UTF-16 surrogate")
	ErrMissingQuote     = errors.New("jstr: missing closing quote")
	ErrTrailingData     = errors.New("jstr: trailing data after closing quote")
	ErrInvalidUTF8      = errors.New("jstr: invalid UTF-8")
)

// Error 携带操作、哨兵类别与偏移。
type Error struct {
	Op     string
	Err    error
	Offset int
}

func (e *Error) Error() string             { return fmt.Sprintf("%s: %v at offset %d", e.Op, e.Err, e.Offset) }
func (e *Error) Unwrap() error             { return e.Err }
func fail(o string, e error, n int) *Error { return &Error{o, e, n} }

const (
	sOpen = iota
	sText
	sBack
	sU
	sMB
	sLow
	sLowBack
	sLowU
	sDone
)

// Decoder 是流式严格解码器；每个输入字节在状态机中恰处理一次，不回溯。
// checked 为被检查字节总数；非导出，同包测试可直接读取。
type Decoder struct {
	out                     strings.Builder
	st                      int
	off, escS, lowS, hiS    int
	hn, mlen, mneed, mstart int
	hex, hi                 rune
	mb                      [4]byte
	checked                 int64
	err                     *Error
}

// NewDecoder 创建解码器；首字节必须是 '"'。
func NewDecoder() *Decoder { return &Decoder{} }

// Checked 返回被检查字节总数（每字节一次）。
func (d *Decoder) Checked() int64 { return d.checked }

// String 返回已解码内容（出错后不保证完整）。
func (d *Decoder) String() string { return d.out.String() }

func (d *Decoder) bad(e *Error) {
	if d.err == nil {
		d.err = e
	}
}

// Write 喂入字节推进状态机；出错后为粘滞错误。
func (d *Decoder) Write(p []byte) (int, error) {
	for i, c := range p {
		if d.err != nil {
			return i, d.err
		}
		d.checked++
		o := d.off
		d.off++
		switch d.st {
		case sOpen:
			if c != '"' {
				d.bad(fail("decode", ErrNotString, o))
			}
			d.st = sText
		case sText:
			d.text(c, o)
		case sBack:
			if c == 'u' {
				d.hn, d.hex, d.st = 0, 0, sU
			} else if r, ok := esc.SimpleEscape(c); ok {
				d.out.WriteRune(r)
				d.st = sText
			} else {
				d.bad(fail("decode", ErrInvalidEscape, d.escS))
			}
		case sU:
			d.uhex(c, o, false)
		case sMB:
			d.cont(c)
		case sLow:
			if c != '\\' {
				d.bad(fail("decode", ErrLoneSurrogate, o))
			} else {
				d.lowS, d.st = o, sLowBack
			}
		case sLowBack:
			if c != 'u' {
				d.bad(fail("decode", ErrLoneSurrogate, d.lowS))
			} else {
				d.hn, d.hex, d.st = 0, 0, sLowU
			}
		case sLowU:
			d.uhex(c, o, true)
		case sDone:
			d.bad(fail("decode", ErrTrailingData, o))
		}
	}
	if d.err != nil {
		return 0, d.err
	}
	return len(p), nil
}

func (d *Decoder) text(c byte, off int) {
	switch {
	case c == '"':
		d.st = sDone
	case c == '\\':
		d.escS, d.st = off, sBack
	case c < 0x20:
		d.bad(fail("decode", ErrControlChar, off))
	case c < 0x80:
		d.out.WriteByte(c)
	case c >= 0xC2 && c <= 0xF4:
		d.mb[0], d.mlen, d.mstart = c, 1, off
		d.mneed = 2 + b2i(c >= 0xE0) + b2i(c >= 0xF0)
		d.st = sMB
	default:
		d.bad(fail("decode", ErrInvalidUTF8, off))
	}
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (d *Decoder) cont(c byte) {
	if c&0xC0 != 0x80 {
		d.bad(fail("decode", ErrInvalidUTF8, d.mstart))
		return
	}
	d.mb[d.mlen] = c
	d.mlen++
	if d.mlen < d.mneed {
		return
	}
	r, n := utf8.DecodeRune(d.mb[:d.mneed])
	if r == utf8.RuneError || n != d.mneed {
		d.bad(fail("decode", ErrInvalidUTF8, d.mstart))
		return
	}
	d.out.WriteRune(r)
	d.mlen, d.st = 0, sText
}

func (d *Decoder) uhex(c byte, o int, low bool) {
	v, ok := esc.HexVal(c)
	if !ok {
		d.bad(fail("decode", ErrBadUnicodeEscape, o))
		return
	}
	d.hex = d.hex<<4 | rune(v)
	d.hn++
	if d.hn < 4 {
		return
	}
	r := d.hex
	d.hn, d.hex = 0, 0
	if low {
		if !esc.IsLowSurrogate(r) {
			d.bad(fail("decode", ErrLoneSurrogate, d.lowS))
			return
		}
		d.out.WriteRune(esc.Pair(d.hi, r))
		d.hi, d.st = 0, sText
		return
	}
	switch {
	case esc.IsHighSurrogate(r):
		d.hi, d.hiS, d.st = r, d.escS, sLow
	case esc.IsLowSurrogate(r):
		d.bad(fail("decode", ErrLoneSurrogate, d.escS))
	default:
		d.out.WriteRune(r)
		d.st = sText
	}
}

// Close 表示输入结束，检查未闭合引号、悬空转义、孤立代理与截断多字节序列。
func (d *Decoder) Close() error {
	if d.err == nil {
		switch {
		case d.mlen > 0:
			d.err = fail("decode", ErrInvalidUTF8, d.mstart)
		case d.st == sDone:
		case d.st == sText:
			d.err = fail("decode", ErrMissingQuote, d.off)
		case d.st == sBack:
			d.err = fail("decode", ErrInvalidEscape, d.escS)
		case d.st == sU:
			d.err = fail("decode", ErrBadUnicodeEscape, d.escS)
		case d.st == sLow:
			d.err = fail("decode", ErrLoneSurrogate, d.hiS)
		case d.st == sLowBack:
			d.err = fail("decode", ErrLoneSurrogate, d.lowS)
		case d.st == sLowU:
			d.err = fail("decode", ErrBadUnicodeEscape, d.lowS)
		default:
			d.err = fail("decode", ErrNotString, d.off)
		}
	}
	return d.err
}

// Decode 严格解码一个含两端引号的 JSON 字符串字面量。
func Decode(lit []byte) (string, error) {
	d := NewDecoder()
	if _, err := d.Write(lit); err != nil {
		return "", err
	}
	if err := d.Close(); err != nil {
		return "", err
	}
	return d.String(), nil
}

const hx = "0123456789abcdef"

// Encode 编码字符串。若 s 含非法 UTF-8，返回 Op="encode"、Err=ErrInvalidUTF8、
// Offset 为首个非法字节偏移的 *Error；不静默替换为 U+FFFD。
func Encode(s string) ([]byte, error) {
	b := make([]byte, 0, len(s)+2)
	b = append(b, '"')
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && n == 1 {
			return nil, fail("encode", ErrInvalidUTF8, i)
		}
		switch r {
		case '"':
			b = append(b, '\\', '"')
		case '\\':
			b = append(b, '\\', '\\')
		case '\b':
			b = append(b, '\\', 'b')
		case '\f':
			b = append(b, '\\', 'f')
		case '\n':
			b = append(b, '\\', 'n')
		case '\r':
			b = append(b, '\\', 'r')
		case '\t':
			b = append(b, '\\', 't')
		default:
			if r < 0x20 {
				b = append(b, '\\', 'u', '0', '0', hx[byte(r)>>4], hx[byte(r)&0xF])
			} else {
				b = append(b, s[i:i+n]...)
			}
		}
		i += n
	}
	return append(b, '"'), nil
}
