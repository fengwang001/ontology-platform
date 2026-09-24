// Package jstr 实现严格的单个 JSON 字符串字面量编解码（RFC 8259）。
//
// 它只处理两个双引号之间的字符串部分，不解析对象或数组。
package jstr

import (
	"errors"
	"fmt"
	"unicode/utf8"

	"ontology/esc"
)

// 五类彼此可判定的解码错误（另含代理对错误，归入 ErrBadSurrogate）。
var (
	ErrControl      = errors.New("jstr: unescaped control character")
	ErrBadEscape    = errors.New("jstr: invalid escape sequence")
	ErrBadUnicode   = errors.New("jstr: invalid \\u escape")
	ErrBadSurrogate = errors.New("jstr: invalid UTF-16 surrogate pair")
	ErrInvalidUTF8  = errors.New("jstr: invalid UTF-8")
	ErrUnterminated = errors.New("jstr: unterminated string literal")
	ErrTrailing     = errors.New("jstr: trailing bytes after closing quote")
)

// SyntaxError 携带错误的字节偏移（从输入第 0 字节计）。
type SyntaxError struct {
	Offset int
	Err    error
}

func (e *SyntaxError) Error() string { return fmt.Sprintf("%s at byte %d", e.Err, e.Offset) }
func (e *SyntaxError) Unwrap() error { return e.Err }

// Decode 严格解码一个含两端引号的 JSON 字符串字面量。
func Decode(lit []byte) (string, error) {
	var d Decoder
	if _, err := d.Write(lit); err != nil {
		return "", err
	}
	if err := d.Close(); err != nil {
	return "", err
	}
	return string(d.out), nil
}

const lowerHex = "0123456789abcdef"

// Encode 将字符串编码为含两端引号的字面量。只转义 "、\、U+0000–U+001F；
// /、DEL(U+007F)、U+2028/U+2029 及其他非 ASCII 一律原样输出。
// 若 s 含非法 UTF-8 字节，返回包装 ErrInvalidUTF8 的 SyntaxError，不静默替换。
func Encode(s string) ([]byte, error) {
	b := make([]byte, 0, len(s)+2)
	b = append(b, '"')
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '"':
			b = append(b, '\\', '"')
		case c == '\\':
			b = append(b, '\\', '\\')
		case c < 0x20:
			b = append(b, '\\')
			switch c {
			case '\b':
				b = append(b, 'b')
			case '\f':
				b = append(b, 'f')
			case '\n':
				b = append(b, 'n')
			case '\r':
				b = append(b, 'r')
			case '\t':
				b = append(b, 't')
			default:
				b = append(b, 'u', '0', '0', lowerHex[c>>4], lowerHex[c&0xF])
			}
		default:
			r, size := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && size == 1 {
				return nil, &SyntaxError{Offset: i, Err: ErrInvalidUTF8}
			}
			b = append(b, s[i:i+size]...)
			i += size
			continue
		}
		i++
	}
	b = append(b, '"')
	return b, nil
}
