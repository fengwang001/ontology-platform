// Package mtype parses and normalizes media types (type/subtype + params).
package mtype

import (
	"errors"
	"fmt"
	"strings"
)

var ErrMissingSlash, ErrEmptySubtype, ErrParamMissingEq, ErrUnclosedQuote, ErrBadQ = errors.New("missing '/'"), errors.New("empty type or subtype"),
	errors.New("parameter missing '='"), errors.New("unclosed quote"), errors.New("invalid q value")

// Type is a normalized media type; Q is in thousandths (1000 == 1.0).
type Type struct {
	Type, Subtype string
	Params        map[string]string
	Q             int
}

// Parse parses a list, dropping invalid items; errors carry the item index.
func Parse(header string) ([]Type, []error) {
	var types []Type
	var errs []error
	items, _ := split(header, ',', false)
	for i, item := range items {
		if t, err := ParseOne(item); err != nil {
			errs = append(errs, fmt.Errorf("item %d: %w", i, err))
		} else {
			types = append(types, t)
		}
	}
	return types, errs
}

func ParseOne(item string) (Type, error) {
	parts, closed := split(strings.TrimSpace(item), ';', true)
	if !closed {
		return Type{}, ErrUnclosedQuote
	}
	slash := strings.IndexByte(parts[0], '/')
	if slash < 0 {
		return Type{}, ErrMissingSlash
	}
	typ, sub := strings.ToLower(strings.TrimSpace(parts[0][:slash])), strings.ToLower(strings.TrimSpace(parts[0][slash+1:]))
	if typ == "" || sub == "" {
		return Type{}, ErrEmptySubtype
	}
	t := Type{Type: typ, Subtype: sub, Params: map[string]string{}, Q: 1000}
	for _, p := range parts[1:] {
		eq := strings.IndexByte(p, '=')
		if eq < 0 {
			return Type{}, ErrParamMissingEq
		}
		name, val := strings.ToLower(strings.TrimSpace(p[:eq])), strings.TrimSpace(p[eq+1:])
		if name != "q" {
			t.Params[name] = val
			continue
		}
		q, err := parseQ(val)
		if err != nil {
			return Type{}, err
		}
		t.Q = q
	}
	return t, nil
}

func parseQ(s string) (int, error) {
	if s == "0" || s == "1" {
		return 1000 * int(s[0]-'0'), nil
	}
	if len(s) < 3 || len(s) > 5 || s[1] != '.' || s[0] != '0' && s[0] != '1' {
		return 0, ErrBadQ
	}
	v := 0
	for _, c := range s[2:] {
		if c < '0' || c > '9' {
			return 0, ErrBadQ
		}
		v = v*10 + int(c-'0')
	}
	v *= [...]int{1000, 100, 10, 1}[len(s)-2]
	if s[0] == '1' {
		if v != 0 {
			return 0, ErrBadQ
		}
		return 1000, nil
	}
	return v, nil
}

// split splits s on sep outside quotes; unq strips quotes/escapes.
func split(s string, sep byte, unq bool) ([]string, bool) {
	var out []string
	var cur []byte
	inQ, esc := false, false
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case esc:
			if !unq {
				cur = append(cur, '\\')
			}
			cur = append(cur, c)
			esc = false
		case c == '\\' && inQ:
			esc = true
		case c == '"':
			inQ = !inQ
			if !unq {
				cur = append(cur, c)
			}
		case c == sep && !inQ:
			out = append(out, string(cur))
			cur = nil
		default:
			cur = append(cur, c)
		}
	}
	return append(out, string(cur)), !inQ && !esc
}
