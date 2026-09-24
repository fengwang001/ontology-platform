package mtype

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrMissingSlash  = errors.New("missing slash")
	ErrEmptySubtype  = errors.New("empty subtype")
	ErrMissingEqual  = errors.New("missing parameter equals")
	ErrUnclosedQuote = errors.New("unclosed quoted parameter value")
)

type ParseError struct {
	Index int
	Kind  error
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("media type item %d: %v", e.Index, e.Kind)
}

func (e *ParseError) Unwrap() error { return e.Kind }

type MediaType struct {
	Type, Subtype string
	Params        map[string]string
}

func Parse(s string, index int) (MediaType, error) {
	base, parts, err := splitParams(s, index)
	if err != nil {
		return MediaType{}, err
	}
	slash := strings.IndexByte(base, '/')
	if slash < 0 {
		return MediaType{}, &ParseError{index, ErrMissingSlash}
	}
	typ := strings.ToLower(strings.TrimSpace(base[:slash]))
	subtype := strings.ToLower(strings.TrimSpace(base[slash+1:]))
	if typ == "" {
		return MediaType{}, &ParseError{index, ErrMissingSlash}
	}
	if subtype == "" {
		return MediaType{}, &ParseError{index, ErrEmptySubtype}
	}
	params := make(map[string]string, len(parts))
	for _, part := range parts {
		eq := strings.IndexByte(part, '=')
		if eq < 0 {
			return MediaType{}, &ParseError{index, ErrMissingEqual}
		}
		name := strings.ToLower(strings.TrimSpace(part[:eq]))
		value, err := unquote(strings.TrimSpace(part[eq+1:]))
		if err != nil {
			return MediaType{}, &ParseError{index, err}
		}
		params[name] = value
	}
	return MediaType{Type: typ, Subtype: subtype, Params: params}, nil
}

func EqualParams(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for name, value := range a {
		if other, ok := b[name]; !ok || other != value {
			return false
		}
	}
	return true
}

func splitParams(s string, index int) (string, []string, error) {
	var parts []string
	start := 0
	quoted := false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			if quoted && i+1 < len(s) {
				i++
			}
		case '"':
			quoted = !quoted
		case ';':
			if !quoted {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	if quoted {
		return "", nil, &ParseError{index, ErrUnclosedQuote}
	}
	if len(parts) == 0 {
		return strings.TrimSpace(s), nil, nil
	}
	return strings.TrimSpace(parts[0]), append(parts[1:], s[start:]), nil
}

func unquote(value string) (string, error) {
	if len(value) < 2 || value[0] != '"' {
		return value, nil
	}
	if value[len(value)-1] != '"' {
		return "", ErrUnclosedQuote
	}
	var b strings.Builder
	for i := 1; i < len(value)-1; i++ {
		if value[i] == '\\' && i+1 < len(value)-1 {
			i++
		}
		b.WriteByte(value[i])
	}
	return b.String(), nil
}
