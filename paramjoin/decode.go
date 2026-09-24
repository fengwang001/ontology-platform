package paramjoin

import (
	"strings"
	"unicode/utf8"
)

func decodeFirst(value string) (string, error) {
	first := strings.IndexByte(value, '\'')
	if first < 0 {
		return "", ErrSyntax
	}
	secondRel := strings.IndexByte(value[first+1:], '\'')
	if secondRel < 0 {
		return "", ErrSyntax
	}
	second := first + 1 + secondRel
	charset := strings.ToLower(value[:first])
	payload := value[second+1:]
	if charset != "utf-8" && charset != "iso-8859-1" {
		return "", CharsetError{Charset: value[:first]}
	}
	data, err := percentDecode(payload)
	if err != nil {
		return "", err
	}
	if charset == "iso-8859-1" {
		runes := make([]rune, len(data))
		for i, b := range data {
			runes[i] = rune(b)
		}
		return string(runes), nil
	}
	if !utf8.Valid(data) {
		return "", ErrEncoding
	}
	return string(data), nil
}

func percentDecode(s string) ([]byte, error) {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			out = append(out, s[i])
			continue
		}
		if i+2 >= len(s) || i+1 >= len(s) || !isHex(s[i+1]) || !isHex(s[i+2]) {
			return nil, ErrSyntax
		}
		out = append(out, hexValue(s[i+1])<<4|hexValue(s[i+2]))
		i += 2
	}
	return out, nil
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func hexValue(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	default:
		return c - 'A' + 10
	}
}
