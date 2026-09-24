package paramlex

import (
	"errors"
	"strings"
	"unicode"
)

var ErrSyntax = errors.New("invalid content-disposition syntax")

type RawParam struct {
	Name     string
	Segment  int
	Extended bool
	Value    string
}

func Parse(header string) (string, []RawParam, error) {
	pos, end := 0, strings.IndexAny(header, "; \t")
	if end < 0 {
		end = len(header)
	}
	disposition := strings.ToLower(header[:end])
	if disposition == "" || strings.ContainsAny(disposition, "\x00-\x1f\x7f") {
		return "", nil, ErrSyntax
	}
	var params []RawParam
	for pos < len(header) {
		pos = skipOWS(header, pos)
		if pos >= len(header) {
			break
		}
		if header[pos] != ';' {
			return "", nil, ErrSyntax
		}
		pos = skipOWS(header, pos+1)
		nameStart := pos
		for pos < len(header) && !strings.ContainsRune("=; \t", rune(header[pos])) {
			pos++
		}
		if pos == nameStart || pos >= len(header) || header[pos] != '=' {
			return "", nil, ErrSyntax
		}
		rawName := strings.ToLower(header[nameStart:pos])
		pos = skipOWS(header, pos+1)
		value, next, err := readValue(header, pos)
		if err != nil {
			return "", nil, err
		}
		param, err := makeParam(rawName, value)
		if err != nil {
			return "", nil, err
		}
		if duplicate(params, param) {
			return "", nil, ErrSyntax
		}
		params = append(params, param)
		pos = next
	}
	return disposition, params, nil
}

func readValue(header string, pos int) (string, int, error) {
	if pos >= len(header) {
		return "", pos, ErrSyntax
	}
	if header[pos] == '"' {
		var b strings.Builder
		pos++
		for pos < len(header) {
			switch c := header[pos]; {
			case c == '"':
				return b.String(), skipOWS(header, pos+1), nil
			case c == '\\':
				pos++
				if pos >= len(header) || ctl(header[pos]) {
					return "", pos, ErrSyntax
				}
				b.WriteByte(header[pos])
			case ctl(c):
				return "", pos, ErrSyntax
			default:
				b.WriteByte(c)
			}
			pos++
		}
		return "", pos, ErrSyntax
	}
	start := pos
	for pos < len(header) && header[pos] != ';' {
		c := header[pos]
		if c == '=' || c == '"' || ctl(c) {
			return "", pos, ErrSyntax
		}
		pos++
	}
	if start == pos {
		return "", pos, ErrSyntax
	}
	return header[start:pos], pos, nil
}

func makeParam(rawName, value string) (RawParam, error) {
	if rawName == "" || strings.Count(rawName, "*") > 2 || strings.Contains(rawName, "**") {
		return RawParam{}, ErrSyntax
	}
	extended := strings.HasSuffix(rawName, "*")
	if extended {
		rawName = rawName[:len(rawName)-1]
	}
	segment := 0
	if star := strings.IndexByte(rawName, '*'); star >= 0 {
		rest := rawName[star+1:]
		if star == 0 || strings.ContainsRune(rest, '*') || rest == "" {
			return RawParam{}, ErrSyntax
		}
		for _, c := range rest {
			if c < '0' || c > '9' {
				return RawParam{}, ErrSyntax
			}
			segment = segment*10 + int(c-'0')
		}
		rawName = rawName[:star]
	}
	if rawName == "" || strings.ContainsAny(rawName, "*' \t") || ctl(rawName[0]) {
		return RawParam{}, ErrSyntax
	}
	return RawParam{Name: rawName, Segment: segment, Extended: extended, Value: value}, nil
}

func duplicate(params []RawParam, param RawParam) bool {
	for _, seen := range params {
		if seen == param {
			return true
		}
	}
	return false
}

func ctl(c byte) bool { return c < 0x20 || c == 0x7f }

func skipOWS(s string, pos int) int {
	for pos < len(s) && (s[pos] == ' ' || s[pos] == '\t') {
		pos++
	}
	return pos
}
