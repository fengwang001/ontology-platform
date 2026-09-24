// Package jstr 严格编解码单个 JSON 字符串字面量（含两端引号），
// 遵循 RFC 8259。仅依赖标准库与同工程的 esc 包。
package jstr

import (
	"errors"
	"fmt"

	"ontology/esc"
)

// 五类彼此可判定的哨兵错误；用 errors.Is 判定。
var (
	ErrControl      = errors.New("jstr: unescaped control character")
	ErrBadEscape    = errors.New("jstr: invalid escape sequence")
	ErrBadUnicode   = errors.New("jstr: invalid \\u escape")
	ErrMissingQuote = errors.New("jstr: missing closing quote")
	ErrTrailing     = errors.New("jstr: bytes after closing quote")
	ErrNotQuoted    = errors.New("jstr: literal must start with a quote")
	ErrInvalidUTF8  = errors.New("jstr: invalid UTF-8")
)

// DecodeError 携带错误类别（Err 可与哨兵用 errors.Is 比较）与字节偏移。
type DecodeError struct {
	Err    error
	Offset int
}

func (e *DecodeError) Error() string { return fmt.Sprintf("%s at byte %d", e.Err, e.Offset) }
func (e *DecodeError) Unwrap() error { return e.Err }

func fail(err error, off int) error { return &DecodeError{Err: err, Offset: off} }

// Decode 严格解码一个带双引号的 JSON 字符串字面量。
func Decode(lit []byte) (string, error) {
	if len(lit) == 0 || lit[0] != '"' {
		return "", fail(ErrNotQuoted, 0)
	}
	var d Decoder
	out, err := d.Write(lit)
	if err != nil {
		return "", err
	}
	tail, err := d.Close()
	if err != nil {
		return "", err
	}
	return string(append(out, tail...)), nil
}

// Decoder 按字节流式解码一个字符串字面量。任意切分点必须与整体解码一致。
type Decoder struct {
	state   int // 0=体, 1=转义后, 2..5=\u 的4位hex, 6=高代理后须反斜杠, 7..10=低代理4位hex, 11=原始多字节
	hex     [4]byte
	hi      uint16
	hiAt    int // 挂起高代理转义起点
	raw     []byte
	need    int // 原始多字节剩余续字节数
	closed  bool
	checked int // 非导出：字节被检查的总次数
}

// Checks 返回字节被检查的总次数（测试断言线性复杂度用）。
func (d *Decoder) Checks() int { return d.checked }

// Write 喂入任意长度的片段，返回新解码出的 UTF-8 字节。
func (d *Decoder) Write(p []byte) ([]byte, error) {
	var out []byte
	if d.closed {
		return nil, fail(ErrTrailing, d.checked)
	}
	for off := 0; off < len(p); off++ {
		b := p[off]
		pos := d.checked
		d.checked++
		switch {
		case d.state >= 7 && d.state <= 10: // 低代理 \u 的第 1..4 位
			d.hex[d.state-7] = b
			if !esc.IsHexDigit(b) {
				return nil, fail(ErrBadUnicode, d.hiAt+6)
			}
			if d.state == 10 {
				lo, _ := esc.DecodeU4(d.hex[:])
				if !esc.IsLowSurrogate(lo) {
					return nil, fail(ErrBadUnicode, d.hiAt+6)
				}
				r := esc.CombineSurrogates(d.hi, lo)
				var buf [4]byte
				n := encRune(buf[:], r)
				out = append(out, buf[:n]...)
				d.state = 0
			} else {
				d.state++
			}
		case d.state == 6: // 高代理后必须紧跟 '\'
			if b != '\\' {
				return nil, fail(ErrBadUnicode, pos)
			}
			d.state = 7
		case d.state >= 2 && d.state <= 5: // 首个 \u 的第 1..4 位
			d.hex[d.state-2] = b
			if !esc.IsHexDigit(b) {
				return nil, fail(ErrBadUnicode, pos)
			}
			if d.state < 5 {
				d.state++
				continue
			}
			v, _ := esc.DecodeU4(d.hex[:])
			switch {
			case esc.IsHighSurrogate(v):
				d.hi, d.hiAt, d.state = v, pos-2, 6
			case esc.IsLowSurrogate(v):
				return nil, fail(ErrBadUnicode, pos-6)
			default:
				var buf [4]byte
				n := encRune(buf[:], rune(v))
				out = append(out, buf[:n]...)
				d.state = 0
			}
		case d.state == 1: // 反斜杠之后
			switch {
			case esc.IsSimpleEscape(b):
				out = append(out, esc.UnescapeSimple(b))
				d.state = 0
			case b == 'u':
				d.state = 2
			default:
				return nil, fail(ErrBadEscape, pos-1)
			}
		case d.state == 11: // 原始多字节 UTF-8
			d.raw = append(d.raw, b)
			if b&0xC0 != 0x80 {
				return nil, fail(ErrInvalidUTF8, pos-len(d.raw)+1)
			}
			d.need--
			if d.need == 0 {
				r := decodeRune(d.raw)
				if r == -1 {
					return nil, fail(ErrInvalidUTF8, pos-len(d.raw)+1)
				}
				out = append(out, d.raw...)
				d.raw, d.state = d.raw[:0], 0
			}
		default: // 体
			switch {
			case b == '"':
				d.closed = true
				if off+1 < len(p) {
					return nil, fail(ErrTrailing, pos+1)
				}
			case b == '\\':
				d.state = 1
			case b < 0x20:
				return nil, fail(ErrControl, pos)
			case b < 0x80:
				out = append(out, b)
			default:
				need, ok := leadLen(b)
				if !ok {
					return nil, fail(ErrInvalidUTF8, pos)
				}
				d.raw = append(d.raw[:0], b)
				d.need, d.state = need-1, 11
			}
		}
	}
	return out, nil
}

// Close 表示输入结束；缺少结尾引号时返回错误。
func (d *Decoder) Close() ([]byte, error) {
	if d.closed {
		return nil, nil
	}
	switch d.state {
	case 6: // 高代理后期待 '\'
		return nil, fail(ErrBadUnicode, d.checked)
	case 7, 8, 9, 10: // 低代理 \u 未完成：错误归于后继单元起点
		return nil, fail(ErrBadUnicode, d.hiAt+6)
	case 2, 3, 4, 5: // 首个 \u 不足 4 位：错误指向该反斜杠
		return nil, fail(ErrBadUnicode, d.checked-d.state+1)
	case 1: // 孤立反斜杠
		return nil, fail(ErrBadEscape, d.checked-1)
	case 11:
		return nil, fail(ErrInvalidUTF8, d.checked-len(d.raw))
	}
	return nil, fail(ErrMissingQuote, d.checked)
}

func leadLen(b byte) (int, bool) {
	switch {
	case b&0xE0 == 0xC0:
		if b < 0xC2 {
			return 0, false
		}
		return 2, true
	case b&0xF0 == 0xE0:
		return 3, true
	case b&0xF8 == 0xF0:
		if b > 0xF4 {
			return 0, false
		}
		return 4, true
	}
	return 0, false
}

// decodeRune 解码一个已知长度合法前缀结构的 UTF-8 序列；非法返回 -1。
func decodeRune(p []byte) rune {
	var r rune
	switch len(p) {
	case 2:
		if p[1]&0xC0 != 0x80 {
			return -1
		}
		r = rune(p[0]&0x1F)<<6 | rune(p[1]&0x3F)
		if r < 0x80 {
			return -1
		}
	case 3:
		for _, b := range p[1:] {
			if b&0xC0 != 0x80 {
				return -1
			}
		}
		r = rune(p[0]&0x0F)<<12 | rune(p[1]&0x3F)<<6 | rune(p[2]&0x3F)
		if r < 0x800 || r >= 0xD800 && r <= 0xDFFF {
			return -1
		}
		if p[0] == 0xE0 && p[1] < 0xA0 {
			return -1
		}
	case 4:
		for _, b := range p[1:] {
			if b&0xC0 != 0x80 {
				return -1
			}
		}
		r = rune(p[0]&0x07)<<18 | rune(p[1]&0x3F)<<12 | rune(p[2]&0x3F)<<6 | rune(p[3]&0x3F)
		if r < 0x10000 || r > 0x10FFFF {
			return -1
		}
		if p[0] == 0xF0 && p[1] < 0x90 || p[0] == 0xF4 && p[1] > 0x8F {
			return -1
		}
	default:
		return -1
	}
	return r
}

func encRune(p []byte, r rune) int {
	switch {
	case r < 0x80:
		p[0] = byte(r)
		return 1
	case r < 0x800:
		p[0] = byte(r>>6) | 0xC0
		p[1] = byte(r&0x3F) | 0x80
		return 2
	case r < 0x10000:
		p[0] = byte(r>>12) | 0xE0
		p[1] = byte(r>>6&0x3F) | 0x80
		p[2] = byte(r&0x3F) | 0x80
		return 3
	default:
		p[0] = byte(r>>18) | 0xF0
		p[1] = byte(r>>12&0x3F) | 0x80
		p[2] = byte(r>>6&0x3F) | 0x80
		p[3] = byte(r&0x3F) | 0x80
		return 4
	}
}

// Encode 用最小转义编码 s，返回带引号的字面量。
// 若 s 含非法 UTF-8 字节，返回 ErrInvalidUTF8，绝不静默替换为 U+FFFD。
func Encode(s string) ([]byte, error) {
	out := make([]byte, 0, len(s)+2)
	out = append(out, '"')
	for i := 0; i < len(s); {
		b := s[i]
		if b < 0x80 {
			switch {
			case b == '"':
				out = append(out, '\\', '"')
			case b == '\\':
				out = append(out, '\\', '\\')
			case b == '\b':
				out = append(out, '\\', 'b')
			case b == '\f':
				out = append(out, '\\', 'f')
			case b == '\n':
				out = append(out, '\\', 'n')
			case b == '\r':
				out = append(out, '\\', 'r')
			case b == '\t':
				out = append(out, '\\', 't')
			case b < 0x20:
				out = append(out, '\\', 'u', '0', '0')
				h := esc.EncodeHex4(uint16(b))
				out = append(out, h[2], h[3])
			default:
				out = append(out, b)
			}
			i++
			continue
		}
		need, ok := leadLen(b)
		if !ok || i+need > len(s) {
			return nil, ErrInvalidUTF8
		}
		chunk := s[i : i+need]
		if decodeRune([]byte(chunk)) == -1 {
			return nil, ErrInvalidUTF8
		}
		out = append(out, chunk...)
		i += need
	}
	out = append(out, '"')
	return out, nil
}
