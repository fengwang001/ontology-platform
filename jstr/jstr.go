// Package jstr 实现单个 JSON 字符串字面量（含两端引号）的严格编解码。
package jstr

import (
	"errors"
	"unicode/utf8"

	"ontology/esc"
)

var (
	ErrMissingQuote = errors.New("jstr: missing opening or closing quote")
	ErrTrailing     = errors.New("jstr: bytes after closing quote")
	ErrControl      = errors.New("jstr: unescaped control character")
	ErrBadEscape    = errors.New("jstr: invalid escape sequence")
	ErrBadUnicode   = errors.New("jstr: invalid \\u escape")
	ErrSurrogate    = errors.New("jstr: invalid UTF-16 surrogate pair")
	ErrInvalidUTF8  = errors.New("jstr: invalid UTF-8")
)

// SyntaxError 携带错误类型哨兵与首个出错字节的偏移。
type SyntaxError struct {
	Offset int
	Err    error
}

func (e *SyntaxError) Error() string { return e.Err.Error() }
func (e *SyntaxError) Unwrap() error { return e.Err }

// Decoder 是增量状态机：每个输入字节恰好检查一次，可按任意边界切分喂入。
type Decoder struct {
	out               []byte
	phase             int // 0 起始引号, 1 字符串体, 2 已闭合, 3 终结
	esc               int // 0 普通, 1 反斜杠后, 2..5 为 \u 十六进制位
	hex               rune
	needLow           bool
	pendOff, hi       rune
	uNeed, uStart    int
	uV, uMin         rune
	inspects          int64
	err               *SyntaxError
}

// Inspections 返回被检查的输入字节总数。
func (d *Decoder) Inspections() int64 { return d.inspects }

func (d *Decoder) fail(off int, err error) error {
	if d.err == nil {
		d.err = &SyntaxError{Offset: off, Err: err}
	}
	d.phase = 3
	return d.err
}

// Write 喂入任意长度的一段字面量字节。
func (d *Decoder) Write(p []byte) (int, error) {
	for i := 0; i < len(p); i++ {
		d.inspects++
		off := int(d.inspects) - 1
		b := p[i]
		switch {
		case d.phase == 0:
			if b != '"' {
				return i + 1, d.fail(off, ErrMissingQuote)
			}
			d.phase = 1
		case d.phase == 2:
			return i + 1, d.fail(off, ErrTrailing)
		case d.phase == 3:
			return i, d.err
		case d.uNeed > 0:
			if b < 0x80 || b > 0xBF {
				return i + 1, d.fail(d.uStart, ErrInvalidUTF8)
			}
			d.uV = (d.uV << 6) | rune(b&0x3F)
			d.uNeed--
			if d.uNeed == 0 {
				r := d.uV
				if !utf8.ValidRune(r) || r < d.uMin {
					return i + 1, d.fail(d.uStart, ErrInvalidUTF8)
				}
				var buf [4]byte
				n := utf8.EncodeRune(buf[:], r)
				d.out = append(d.out, buf[:n]...)
			}
		case d.esc == 1:
			if _, err := d.finishSimple(b, off); err != nil {
				return i + 1, err
			}
		case d.esc >= 2:
			if _, err := d.finishHex(b, off); err != nil {
				return i + 1, err
			}
		case b == '"':
			if d.needLow {
				return i + 1, d.fail(int(d.pendOff), ErrSurrogate)
			}
			d.phase = 2
		case b == '\\':
			d.esc = 1
		case b < 0x20:
			return i + 1, d.fail(off, ErrControl)
		default:
			if n, err := d.rawLead(b, off, i, p); err != nil {
				return n, err
			}
			i = n - 1
			d.inspects += int64(d.uNeed)
		}
	}
	return len(p), nil
}

func (d *Decoder) finishSimple(b byte, off int) (int, error) {
	if r, ok := esc.Simple(b); ok {
		var buf [4]byte
		n := utf8.EncodeRune(buf[:], r)
		d.out = append(d.out, buf[:n]...)
		d.esc = 0
		return 0, nil
	}
	if b != 'u' {
		return 0, d.fail(off-1, ErrBadEscape)
	}
	d.esc, d.hex = 2, 0
	return 0, nil
}

func (d *Decoder) finishHex(b byte, off int) (int, error) {
	v, ok := esc.Hex(b)
	if !ok {
		return 0, d.fail(off-d.esc+1, ErrBadUnicode)
	}
	d.hex = (d.hex << 4) | v
	if d.esc < 5 {
		d.esc++
		return 0, nil
	}
	r, d.esc := d.hex, 0
	if d.needLow {
		d.needLow = false
		if r2, ok := esc.Pair(d.hi, r); ok {
			var buf [4]byte
			n := utf8.EncodeRune(buf[:], r2)
			d.out = append(d.out, buf[:n]...)
			return 0, nil
		}
		return 0, d.fail(int(d.pendOff), ErrSurrogate)
	}
	switch {
	case esc.HighSurrogate(r):
		d.needLow, d.pendOff, d.hi = true, rune(off-5), r
	case esc.LowSurrogate(r):
		return 0, d.fail(off-5, ErrSurrogate)
	default:
		var buf [4]byte
		n := utf8.EncodeRune(buf[:], r)
		d.out = append(d.out, buf[:n]...)
	}
	return 0, nil
}

// rawLead 处理当前字节起的一段（可能跨多字节 UTF-8）原始文本，
// 返回新的切片下标（已消费到其前一位）。
func (d *Decoder) rawLead(b byte, off, i int, p []byte) (int, error) {
	if b < 0x80 {
		d.out = append(d.out, b)
		return i + 1, nil
	}
	var v rune
	var size int
	switch {
	case b&0xE0 == 0xC0:
		v, size = rune(b&0x1F), 2
	case b&0xF0 == 0xE0:
		v, size = rune(b&0x0F), 3
	case b&0xF8 == 0xF0:
		v, size = rune(b&0x07), 4
	default:
		return i, d.fail(off, ErrInvalidUTF8)
	}
	j := i + 1
	for k := 1; k < size; k++ {
		if j >= len(p) {
			d.uNeed, d.uStart, d.uV = size-k, off, v
			return j, nil
		}
		c := p[j]
		if c < 0x80 || c > 0xBF {
			return j, d.fail(off, ErrInvalidUTF8)
		}
		v = (v << 6) | rune(c&0x3F)
		j++
	}
	if !utf8.ValidRune(v) || v < minRune(size) {
		return i, d.fail(off, ErrInvalidUTF8)
	}
	var buf [4]byte
	n := utf8.EncodeRune(buf[:], v)
	d.out = append(d.out, buf[:n]...)
	return j, nil
}

func minRune(size int) rune {
	switch size {
	case 2:
		return 0x80
	case 3:
		return 0x800
	default:
		return 0x10000
	}
}

// Close 结束输入；结尾引号缺失或转义、多字节序列被截断时返回错误。
func (d *Decoder) Close() error {
	if d.phase == 2 {
		d.phase = 3
		return nil
	}
	if d.phase == 3 {
		return d.err
	}
	last := int(d.inspects) - 1
	switch {
	case d.needLow:
		return d.fail(int(d.pendOff), ErrSurrogate)
	case d.esc == 1:
		return d.fail(last, ErrBadEscape)
	case d.esc >= 2:
		return d.fail(last-d.esc+2, ErrBadUnicode)
	case d.uNeed > 0:
		return d.fail(d.uStart, ErrInvalidUTF8)
	default:
		return d.fail(last, ErrMissingQuote)
	}
}

// Decode 解码一个含两端引号的 JSON 字符串字面量。
func Decode(lit []byte) (string, error) {
	d := NewDecoder()
	if _, err := d.Write(lit); err != nil {
		return "", err
	}
	if err := d.Close(); err != nil {
		return "", err
	}
	return string(d.out), nil
}

// NewDecoder 创建流式解码器。
func NewDecoder() *Decoder { return &Decoder{} }

// Encode 以最小转义编码字符串并返回含两端引号的字面量。输入含非法 UTF-8
// 时返回包装 ErrInvalidUTF8 的 *SyntaxError，偏移指向首个非法字节；不做替换。
func Encode(s string) ([]byte, error) {
	out := make([]byte, 0, len(s)+2)
	out = append(out, '"')
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			return nil, &SyntaxError{Offset: i, Err: ErrInvalidUTF8}
		}
		switch {
		case r == '"' || r == '\\':
			out = append(out, '\\', byte(r))
		case r == '\b', r == '\f', r == '\n', r == '\r', r == '\t':
			out = append(out, '\\', map[rune]byte{'\b': 'b', '\f': 'f', '\n': 'n', '\r': 'r', '\t': 't'}[r])
		case r < 0x20:
			const hex = "0123456789abcdef"
			out = append(out, '\\', 'u', '0', '0', hex[r>>4], hex[r&0xF])
		default:
			out = append(out, s[i:i+size]...)
		}
		i += size
	}
	return append(out, '"'), nil
}
