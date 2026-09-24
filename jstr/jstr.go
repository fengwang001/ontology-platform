// Package jstr 实现单个 RFC 8259 JSON 字符串字面量（含两端引号）的严格编解码。
package jstr

import (
	"errors"
	"unicode/utf8"

	"ontology/esc"
)

// 五类解码错误彼此可区分；非法 UTF-8 与代理对错误额外区分。
var (
	ErrControlChar   = errors.New("jstr: unescaped control character")
	ErrBadEscape     = errors.New("jstr: unknown escape sequence")
	ErrBadUnicode    = errors.New("jstr: invalid \\u escape")
	ErrMissingQuote  = errors.New("jstr: missing closing quote")
	ErrTrailingBytes = errors.New("jstr: bytes after closing quote")
	ErrSurrogate     = errors.New("jstr: invalid UTF-16 surrogate pair")
	ErrInvalidUTF8   = errors.New("jstr: invalid UTF-8 in literal")
	ErrInvalidUTF8In = errors.New("jstr: invalid UTF-8 in input string")
	ErrEmptyInput    = errors.New("jstr: empty input")
	ErrMissingOpenQ  = errors.New("jstr: missing opening quote")
)

// DecodeError 携带 0 基字节偏移；Is 可穿透到具体哨兵错误。
type DecodeError struct {
	Err    error
	Offset int
}

func (e *DecodeError) Error() string { return e.Err.Error() }
func (e *DecodeError) Unwrap() error { return e.Err }

func fail(err error, off int) error { return &DecodeError{Err: err, Offset: off} }

const (
	stOpen = iota // 等待起始引号
	stBody        // 字面量内容
	stBack        // 刚读到反斜杠
	stU           // \u 已开始，hex 中
	stLow         // 高代理后，等待低代理的反斜杠
	stLowU        // 低代理 \u 已开始，hex 中
	stDone        // 已遇结尾引号
)

// Decoder 是流式严格解码器；字节只检查一次，跨任意切分结果一致。
type Decoder struct {
	state int
	off   int // 已消费的总字节偏移
	unit  int // 当前转义单元起点（反斜杠）偏移
	hex   [6]byte
	nhex  int
	hi    rune
	pend  [4]byte // 跨 chunk 的不完整 UTF-8 前缀
	npend int
	poff  int // pend 首字节的全局偏移
	out   []byte
	err   error

	// checks 为非导出计数器：输入字节被检查的总次数（每字节恰一次）。
	checks int
}

// Checks 返回字节被检查的总次数。
func (d *Decoder) Checks() int { return d.checks }

func (d *Decoder) emitRune(r rune) {
	var buf [4]byte
	n := utf8.EncodeRune(buf[:], r)
	d.out = append(d.out, buf[:n]...)
}

// Write 喂入字面量的任意前缀。
func (d *Decoder) Write(p []byte) (int, error) {
	n := len(p)
	for _, c := range p {
		d.checks++
		if d.err != nil {
			d.off++
			continue
		}
		d.step(c)
		d.off++
	}
	return n, d.err
}

func (d *Decoder) step(c byte) {
	switch d.state {
	case stOpen:
		if c != '"' {
			d.err = fail(ErrMissingOpenQ, d.off)
		} else {
			d.state = stBody
		}
	case stDone:
		d.err = fail(ErrTrailingBytes, d.off)
	case stBody:
		d.body(c)
	case stBack:
		d.back(c)
	case stU, stLowU:
		d.hexByte(c)
	case stLow:
		if c == '\\' {
			d.state = stLowU
			d.nhex = 0
		} else {
			d.err = fail(ErrSurrogate, d.unit)
		}
	}
}

func (d *Decoder) body(c byte) {
	switch {
	case c == '"':
		if d.npend != 0 {
			d.err = fail(ErrInvalidUTF8, d.poff)
			return
		}
		d.state = stDone
	case c == '\\':
		d.unit = d.off
		d.state = stBack
	case c < 0x20:
		d.err = fail(ErrControlChar, d.off)
	default:
		d.utf8Byte(c)
	}
}

func (d *Decoder) utf8Byte(c byte) {
	if d.npend > 0 {
		d.pend[d.npend] = c
		d.npend++
		r, s := utf8.DecodeRune(d.pend[:d.npend])
		if r == utf8.RuneError {
			// s 等于引导字节声称的长度。s<=npend 表示字节已齐仍非法
			// （坏续字节）；否则只是不完整前缀，可继续等待。
			if s <= d.npend {
				d.err = fail(ErrInvalidUTF8, d.poff)
			}
			return
		}
		if s == d.npend {
			d.out = append(d.out, d.pend[:s]...)
			d.npend = 0
		}
		return
	}
	if c < 0x80 {
		d.out = append(d.out, c)
		return
	}
	d.pend[0] = c
	d.npend = 1
	d.poff = d.off
	// DecodeRune 对非法首字节（0x80–0xBF、0xF8–0xFF）立即确定非法。
	if r, s := utf8.DecodeRune(d.pend[:1]); r == utf8.RuneError && s == 1 {
		d.err = fail(ErrInvalidUTF8, d.poff)
	}
}

func (d *Decoder) back(c byte) {
	switch {
	case esc.IsEscapable(c):
		r, _ := esc.SimpleRune(c)
		d.emitRune(r)
		d.state = stBody
	case esc.IsUnicodeStart(c):
		d.state = stU
		d.nhex = 0
	default:
		d.err = fail(ErrBadEscape, d.unit)
	}
}

func (d *Decoder) hexByte(c byte) {
	if _, ok := esc.HexValue(c); !ok {
		d.err = fail(ErrBadUnicode, d.unit)
		return
	}
	d.hex[d.nhex] = c
	d.nhex++
	if d.nhex < 4 {
		return
	}
	v, _ := esc.DecodeHex4(d.hex[:4])
	d.nhex = 0
	d.finishUnicode(v)
}

func (d *Decoder) finishUnicode(v rune) {
	if d.state == stLowU {
		if !esc.IsLowSurrogate(v) {
			d.err = fail(ErrSurrogate, d.unit)
			return
		}
		r, _ := esc.Pair(d.hi, v)
		d.emitRune(r)
		d.state = stBody
		return
	}
	switch {
	case esc.IsHighSurrogate(v):
		d.hi = v
		d.state = stLow
	case esc.IsLowSurrogate(v):
		d.err = fail(ErrSurrogate, d.unit)
	default:
		d.emitRune(v)
		d.state = stBody
	}
}

// Close 表示输入结束；缺少结尾引号等延迟错误在此返回。
func (d *Decoder) Close() error {
	if d.err != nil {
		return d.err
	}
	switch d.state {
	case stDone:
		return nil
	case stOpen:
		return fail(ErrEmptyInput, d.off)
	case stU, stLowU:
		return fail(ErrBadUnicode, d.unit)
	case stLow:
		return fail(ErrSurrogate, d.unit)
	}
	if d.npend != 0 {
		return fail(ErrInvalidUTF8, d.poff)
	}
	return fail(ErrMissingQuote, d.off)
}

// Result 返回已解码字符串。
func (d *Decoder) Result() string { return string(d.out) }

// Decode 严格解码一个含两端引号的 JSON 字符串字面量。
func Decode(lit []byte) (string, error) {
	if len(lit) == 0 {
		return "", fail(ErrEmptyInput, 0)
	}
	var d Decoder
	if _, err := d.Write(lit); err != nil {
		return "", err
	}
	if err := d.Close(); err != nil {
		return "", err
	}
	return d.Result(), nil
}

// Encode 以最小转义编码字符串，返回含两端引号的字面量。
// 若 s 含非法 UTF-8，返回包装 ErrInvalidUTF8In 的错误，绝不静默替换。
func Encode(s string) ([]byte, error) {
	out := make([]byte, 0, len(s)+2)
	out = append(out, '"')
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			return nil, fail(ErrInvalidUTF8In, i)
		}
		out = encodeRune(out, r)
		i += size
	}
	out = append(out, '"')
	return out, nil
}

const hexdigits = "0123456789abcdef"

func encodeRune(out []byte, r rune) []byte {
	switch r {
	case '"':
		return append(out, '\\', '"')
	case '\\':
		return append(out, '\\', '\\')
	case '\b':
		return append(out, '\\', 'b')
	case '\f':
		return append(out, '\\', 'f')
	case '\n':
		return append(out, '\\', 'n')
	case '\r':
		return append(out, '\\', 'r')
	case '\t':
		return append(out, '\\', 't')
	}
	if r < 0x20 {
		return append(out, '\\', 'u', '0', '0', hexdigits[r>>4], hexdigits[r&0xF])
	}
	var buf [4]byte
	n := utf8.EncodeRune(buf[:], r)
	return append(out, buf[:n]...)
}
