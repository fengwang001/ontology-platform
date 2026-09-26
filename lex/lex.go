// Package lex 实现 JSON 严格子集的词法元素解析：数字与字符串。
package lex

import (
	"errors"
	"strconv"
	"strings"
	"unicode/utf16"
)

var ( // 哨兵错误互不相同，可用 errors.Is 判定
	ErrLeadingZero           = errors.New("lex: leading zero")
	ErrEmptyFrac             = errors.New("lex: empty fraction")
	ErrEmptyExp              = errors.New("lex: empty exponent")
	ErrLoneMinus             = errors.New("lex: lone minus")
	ErrBadEscape             = errors.New("lex: bad escape")
	ErrLoneSurrogate         = errors.New("lex: lone surrogate")
	ErrControl, ErrBadNumber = errors.New("lex: raw control character"), errors.New("lex: bad number")
)

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// ParseNumber 逐字符扫描校验严格文法 -?int frac? exp?，值取标准库正确舍入结果。
func ParseNumber(s string) (float64, error) {
	i, n := 0, len(s)
	if i < n && s[i] == '-' {
		i++
	}
	if i >= n {
		return 0, ErrLoneMinus
	}
	switch c := s[i]; {
	case c == '0':
		i++
	case c >= '1' && c <= '9':
		for i < n && isDigit(s[i]) {
			i++
		}
	default:
		return 0, ErrBadNumber
	}
	if i < n && isDigit(s[i]) { // 只有 '0' 分支会剩下数字
		return 0, ErrLeadingZero
	}
	if i < n && s[i] == '.' {
		i++
		if i >= n || !isDigit(s[i]) {
			return 0, ErrEmptyFrac
		}
		for i < n && isDigit(s[i]) {
			i++
		}
	}
	if i < n && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < n && (s[i] == '+' || s[i] == '-') {
			i++
		}
		if i >= n || !isDigit(s[i]) {
			return 0, ErrEmptyExp
		}
		for i < n && isDigit(s[i]) {
			i++
		}
	}
	if i != n {
		return 0, ErrBadNumber
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil { // 含 ErrRange（如 1e999 溢出为 Inf）
		return 0, ErrBadNumber
	}
	return f, nil
}

func hex4(b []byte) (r rune, ok bool) {
	for _, c := range b {
		d := c - '0'
		switch {
		case d <= 9:
		case c >= 'a' && c <= 'f':
			d = c - 'a' + 10
		case c >= 'A' && c <= 'F':
			d = c - 'A' + 10
		default:
			return 0, false
		}
		r = r*16 + rune(d)
	}
	return r, true
}

func ParseString(raw []byte) (string, error) {
	var sb strings.Builder
	for i := 0; i < len(raw); {
		if c := raw[i]; c != '\\' {
			if c < 0x20 {
				return "", ErrControl
			}
			sb.WriteByte(c)
			i++
			continue
		}
		i++
		if i >= len(raw) {
			return "", ErrBadEscape
		}
		if raw[i] != 'u' {
			j := strings.IndexByte(`"\/bfnrt`, raw[i])
			if j < 0 {
				return "", ErrBadEscape
			}
			sb.WriteByte("\"\\/\b\f\n\r\t"[j])
			i++
			continue
		}
		r, ok := rune(0), false
		if i+5 <= len(raw) {
			r, ok = hex4(raw[i+1 : i+5])
		}
		if !ok {
			return "", ErrBadEscape
		}
		i += 5
		if !utf16.IsSurrogate(r) {
			sb.WriteRune(r)
			continue
		}
		r2, ok2 := rune(0), false
		if r < 0xDC00 && i+6 <= len(raw) && raw[i] == '\\' && raw[i+1] == 'u' {
			r2, ok2 = hex4(raw[i+2 : i+6])
		}
		if !ok2 || r2 < 0xDC00 || r2 > 0xDFFF {
			return "", ErrLoneSurrogate
		}
		i += 6
		sb.WriteRune(utf16.DecodeRune(r, r2))
	}
	return sb.String(), nil
}
