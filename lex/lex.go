package lex

import (
	"errors"
	"math"
	"unicode/utf16"
	"unicode/utf8"
)

var (
	ErrLeadingZero, ErrEmptyFrac, ErrEmptyExp, ErrLoneMinus   = errors.New("lex: leading zero is not allowed"), errors.New("lex: fraction part has no digits"), errors.New("lex: exponent part has no digits"), errors.New("lex: lone minus sign")
	ErrBadEscape, ErrLoneUnicode, ErrLoneSurrogate, ErrSyntax = errors.New("lex: unknown or invalid escape"), errors.New("lex: incomplete \\u escape"), errors.New("lex: lone UTF-16 surrogate"), errors.New("lex: invalid syntax")
)
var simpleEsc = map[byte]byte{'"': '"', '\\': '\\', '/': '/', 'b': '\b', 'f': '\f', 'n': '\n', 'r': '\r', 't': '\t'}

func dg(b byte) bool { return b >= '0' && b <= '9' }
func scanDigits(s string, i int) (v float64, j int) {
	for j = i; j < len(s) && dg(s[j]); j++ {
		v = v*10 + float64(s[j]-'0')
	}
	return
}
func ParseNumber(s string) (float64, error) {
	i, n := 0, len(s)
	if n > 0 && s[0] == '-' {
		i = 1
	}
	if i >= n {
		return 0, ErrLoneMinus
	}
	var v float64
	if s[i] == '0' {
		i++
		if i < n && dg(s[i]) {
			return 0, ErrLeadingZero
		}
	} else if s[i] >= '1' && s[i] <= '9' {
		v, i = scanDigits(s, i)
	} else {
		return 0, ErrSyntax
	}
	if i < n && s[i] == '.' {
		f, j := scanDigits(s, i+1)
		if j == i+1 {
			return 0, ErrEmptyFrac
		}
		v, i = v+f/math.Pow10(j-i-1), j
	}
	if i < n && (s[i] == 'e' || s[i] == 'E') {
		st := i + 1
		if st < n && (s[st] == '+' || s[st] == '-') {
			st++
		}
		ev, j := scanDigits(s, st)
		if j == st {
			return 0, ErrEmptyExp
		}
		if s[i+1] == '-' {
			ev = -ev
		}
		v, i = v*math.Pow10(int(ev)), j
	}
	if i != n || math.IsInf(v, 0) || math.IsNaN(v) {
		return 0, ErrSyntax
	}
	if s[0] == '-' {
		v = -v
	}
	return v, nil
}
func hex4(b []byte, i int) (rune, error) {
	if i+4 > len(b) {
		return 0, ErrLoneUnicode
	}
	var r rune
	for k := 0; k < 4; k++ {
		switch c := b[i+k]; {
		case c >= '0' && c <= '9':
			r = r<<4 | rune(c-'0')
		case c >= 'a' && c <= 'f':
			r = r<<4 | rune(c-'a'+10)
		case c >= 'A' && c <= 'F':
			r = r<<4 | rune(c-'A'+10)
		default:
			return 0, ErrLoneUnicode
		}
	}
	return r, nil
}
func ParseString(b []byte) (string, error) {
	n := len(b)
	if n < 2 || b[0] != '"' || b[n-1] != '"' {
		return "", ErrSyntax
	}
	out, tmp := make([]byte, 0, n), [4]byte{}
	for i := 1; i < n; {
		switch c := b[i]; {
		case c == '"':
			if i == n-1 {
				return string(out), nil
			}
			return "", ErrSyntax
		case c < 0x20:
			return "", ErrSyntax
		case c != '\\':
			if c < 0x80 {
				out, i = append(out, c), i+1
			} else if r, sz := utf8.DecodeRune(b[i:]); r == utf8.RuneError {
				return "", ErrSyntax
			} else {
				out, i = append(out, b[i:i+sz]...), i+sz
			}
		default:
			if i+1 >= n {
				return "", ErrSyntax
			}
			e, j := b[i+1], i+2
			if x, ok := simpleEsc[e]; ok {
				out, i = append(out, x), j
				continue
			}
			if e != 'u' {
				return "", ErrBadEscape
			}
			if j+4 > n {
				return "", ErrLoneUnicode
			}
			r, err := hex4(b, j)
			if err != nil {
				return "", err
			}
			j += 4
			if utf16.IsSurrogate(r) {
				if r >= 0xDC00 || j+6 > n || b[j] != '\\' || b[j+1] != 'u' {
					return "", ErrLoneSurrogate
				}
				lo, err := hex4(b, j+2)
				if err != nil || lo < 0xDC00 || lo > 0xDFFF {
					return "", ErrLoneSurrogate
				}
				r, j = 0x10000+(r-0xD800)<<10+(lo-0xDC00), j+6
			}
			sz := utf8.EncodeRune(tmp[:], r)
			out, i = append(out, tmp[:sz]...), j
		}
	}
	return "", ErrSyntax
}
