// Package paramlex 词法切分 Content-Disposition 头部文本，产出原始参数项。
package paramlex

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var ErrSyntax = errors.New("paramlex: syntax error")

type Item struct {
	Name, Raw string
	Seq       int
	Ext       bool
}

func syntaxf(f string, a ...any) error { return fmt.Errorf("%w: %s", ErrSyntax, fmt.Sprintf(f, a...)) }

func Parse(header string) (string, []Item, error) {
	typ, rest, _ := strings.Cut(header, ";")
	if typ = strings.ToLower(strings.TrimSpace(typ)); typ == "" {
		return "", nil, syntaxf("empty disposition type")
	}
	var items []Item
	seen := map[Item]bool{}
	for {
		if rest = strings.TrimLeft(rest, " \t"); rest == "" {
			return typ, items, nil
		}
		eq := strings.IndexByte(rest, '=')
		if eq <= 0 {
			return "", nil, syntaxf("missing = or empty name")
		}
		it, err := parseName(rest[:eq])
		if err == nil {
			it.Raw, rest, err = parseValue(rest[eq+1:])
		}
		if err != nil {
			return "", nil, err
		}
		k := it
		k.Raw = ""
		if seen[k] {
			return "", nil, syntaxf("duplicate parameter %q", it.Name)
		}
		seen[k] = true
		items = append(items, it)
		if rest == "" {
			return typ, items, nil
		}
		if rest[0] != ';' {
			return "", nil, syntaxf("expected ; after value")
		}
		rest = rest[1:]
	}
}

func parseName(s string) (Item, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	p := strings.Split(s, "*")
	it := Item{Seq: -1}
	var num string
	switch {
	case len(p) == 1:
	case len(p) == 2 && p[1] == "":
		it.Ext = true
	case len(p) == 2:
		num = p[1]
	case len(p) == 3 && p[1] != "" && p[2] == "":
		num, it.Ext = p[1], true
	default:
		return it, syntaxf("bad parameter name %q", s)
	}
	it.Name = p[0]
	if num != "" {
		if n, err := strconv.Atoi(num); err == nil && n >= 0 {
			it.Seq = n
		} else {
			return it, syntaxf("bad sequence number %q", num)
		}
	}
	if !isToken(it.Name) {
		return it, syntaxf("bad parameter name %q", s)
	}
	return it, nil
}

func isToken(s string) bool {
	return s != "" && strings.IndexFunc(s, func(r rune) bool {
		return r <= ' ' || r == 0x7f || r == ';' || r == '=' || r == '"'
	}) < 0
}

func parseValue(s string) (val, rest string, err error) {
	if s = strings.TrimLeft(s, " \t"); s == "" {
		return "", "", syntaxf("missing value")
	}
	if s[0] != '"' {
		end := strings.IndexByte(s, ';')
		if end < 0 {
			end = len(s)
		}
		if !isToken(s[:end]) {
			return "", "", syntaxf("bad character in bare token")
		}
		return s[:end], s[end:], nil
	}
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
		} else if s[i] == '"' {
			return b.String(), s[i+1:], nil
		}
		b.WriteByte(s[i])
	}
	return "", "", syntaxf("unterminated quoted string")
}
