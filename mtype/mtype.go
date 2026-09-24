// Package mtype parses and normalizes media types (type/subtype;params).
package mtype

import (
	"fmt"
	"strings"
)

// Kind identifies a parse error category.
type Kind int

const (
	ErrNoSlash Kind = iota + 1
	ErrEmptySubtype
	ErrParamNoEq
	ErrUnclosedQuote
	ErrBadQ
)

// Error is a parse error; Index is the item position in the Accept
// list (-1 when not applicable, e.g. parsing an offered type).
type Error struct {
	Kind  Kind
	Index int
	Text  string
}

func (e *Error) Error() string {
	return fmt.Sprintf("mtype: item %d: kind %d: %q", e.Index, e.Kind, e.Text)
}

// Type is a normalized media type: lowercased type/subtype and param
// names; param values keep case and are unquoted/unescaped.
type Type struct {
	Type    string
	Subtype string
	Params  map[string]string
}

// Parse parses one media type like `text/plain;format=flowed`.
func Parse(s string, index int) (Type, error) {
	fail := func(k Kind) (Type, error) { return Type{}, &Error{Kind: k, Index: index, Text: s} }
	s = strings.TrimSpace(s)
	slash := strings.IndexByte(s, '/')
	if slash < 1 {
		return fail(ErrNoSlash)
	}
	sub, r, _ := strings.Cut(s[slash+1:], ";")
	rest := ";" + r
	sub = strings.ToLower(strings.TrimSpace(sub))
	if sub == "" {
		return fail(ErrEmptySubtype)
	}
	t := Type{Type: strings.ToLower(strings.TrimSpace(s[:slash])), Subtype: sub, Params: map[string]string{}}
	for rest != "" {
		rest = strings.TrimLeft(rest[1:], " \t") // consume ';'
		if rest == "" {
			break
		}
		eq := strings.IndexByte(rest, '=')
		if eq < 0 {
			return fail(ErrParamNoEq)
		}
		name := strings.ToLower(strings.TrimSpace(rest[:eq]))
		rest = strings.TrimLeft(rest[eq+1:], " \t")
		var val string
		if strings.HasPrefix(rest, "\"") {
			v, r, ok := parseQuoted(rest[1:])
			if !ok {
				return fail(ErrUnclosedQuote)
			}
			val, rest = v, strings.TrimLeft(r, " \t")
		} else {
			v, r, _ := strings.Cut(rest, ";")
			val, rest = strings.TrimRight(v, " \t"), ";"+r
		}
		if rest != "" && !strings.HasPrefix(rest, ";") {
			return fail(ErrParamNoEq)
		}
		t.Params[name] = val
	}
	return t, nil
}

// parseQuoted parses a quoted-string body, handling \" escapes.
func parseQuoted(s string) (val, rest string, ok bool) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
			if i >= len(s) {
				return "", "", false
			}
			b.WriteByte(s[i])
		case '"':
			return b.String(), s[i+1:], true
		default:
			b.WriteByte(s[i])
		}
	}
	return "", "", false
}

// ParamsEqual reports whether two param sets are identical.
func ParamsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}
