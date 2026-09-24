// Package paramlex splits a Content-Disposition header into raw items.
package paramlex

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrSyntax marks malformed header text; all syntax errors wrap it.
var ErrSyntax = errors.New("paramlex: syntax error")

// Item is one raw parameter occurrence in the header.
type Item struct {
	Name    string // lowercased base name, without any '*' suffix
	Section int    // continuation index, -1 when the name has no number
	Ext     bool   // trailing '*' marker (extended value)
	Value   string // raw value; quotes and backslash escapes removed
}

// Parse splits header into the lowercased disposition type and raw items.
func Parse(header string) (string, []Item, error) {
	typ, rest := header, ""
	if i := strings.IndexByte(header, ';'); i >= 0 {
		typ, rest = header[:i], header[i:]
	}
	if typ = strings.ToLower(strings.TrimSpace(typ)); typ == "" {
		return "", nil, fmt.Errorf("%w: empty disposition type", ErrSyntax)
	}
	seen := map[string]bool{}
	var items []Item
	for rest != "" {
		rest = strings.TrimLeft(rest[1:], " \t") // consume ';'
		eq, semi := strings.IndexByte(rest, '='), strings.IndexByte(rest, ';')
		if eq < 0 || (semi >= 0 && semi < eq) {
			return "", nil, fmt.Errorf("%w: missing '='", ErrSyntax)
		}
		name := strings.ToLower(strings.TrimSpace(rest[:eq]))
		val, tail, err := parseValue(strings.TrimLeft(rest[eq+1:], " \t"))
		if err != nil {
			return "", nil, err
		}
		it, err := parseName(name, val)
		if err != nil {
			return "", nil, err
		}
		k := fmt.Sprintf("%s|%d|%t", it.Name, it.Section, it.Ext)
		if seen[k] {
			return "", nil, fmt.Errorf("%w: duplicate parameter %q", ErrSyntax, name)
		}
		seen[k] = true
		items = append(items, it)
		rest = tail
	}
	return typ, items, nil
}

func parseValue(s string) (val, rest string, err error) {
	if !strings.HasPrefix(s, "\"") {
		tok := s
		if end := strings.IndexByte(s, ';'); end >= 0 {
			tok, rest = s[:end], s[end:]
		}
		tok = strings.TrimRight(tok, " \t")
		bad := func(r rune) bool { return r <= ' ' || r == 0x7f || r == '=' || r == '"' }
		if strings.IndexFunc(tok, bad) >= 0 {
			return "", "", fmt.Errorf("%w: bad byte in token %q", ErrSyntax, tok)
		}
		return tok, rest, nil
	}
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		switch c := s[i]; {
		case c == '\\' && i+1 < len(s):
			b.WriteByte(s[i+1])
			i++
		case c == '"':
			if r := strings.TrimLeft(s[i+1:], " \t"); r == "" || r[0] == ';' {
				return b.String(), r, nil
			}
			return "", "", fmt.Errorf("%w: junk after quoted value", ErrSyntax)
		default:
			b.WriteByte(c)
		}
	}
	return "", "", fmt.Errorf("%w: unterminated quote", ErrSyntax)
}

func parseName(name, val string) (Item, error) {
	it := Item{Section: -1, Value: val}
	parts := strings.Split(name, "*")
	num := func(s string) (int, bool) {
		n, err := strconv.Atoi(s)
		return n, err == nil && n >= 0 && strconv.Itoa(n) == s
	}
	mid := ""
	if len(parts) > 1 {
		mid = parts[1]
	}
	n, ok := num(mid)
	switch {
	case len(parts) == 1:
		it.Name = parts[0]
	case len(parts) == 2 && parts[1] == "":
		it.Name, it.Ext = parts[0], true
	case len(parts) == 2 && ok:
		it.Name, it.Section = parts[0], n
	case len(parts) == 3 && parts[2] == "" && ok:
		it.Name, it.Section, it.Ext = parts[0], n, true
	}
	bad := func(r rune) bool { return r <= ' ' || r == 0x7f || r == '=' }
	if it.Name == "" || strings.IndexFunc(it.Name, bad) >= 0 {
		return it, fmt.Errorf("%w: bad parameter name %q", ErrSyntax, name)
	}
	return it, nil
}
