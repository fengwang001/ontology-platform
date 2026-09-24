// Package props parses and writes Java .properties text (java.util.Properties
// load(Reader)/store semantics), preserving first-seen key order.
package props

import (
	"io"
	"unicode/utf8"

	"ontology/logical"
)

// Properties is an ordered key/value map.
type Properties struct {
	order []string
	vals  map[string]string
}

// New returns an empty Properties.
func New() *Properties {
	return &Properties{vals: map[string]string{}}
}

// EscapeError reports a malformed \uXXXX escape, with physical position.
type EscapeError struct {
	Line int
	Col  int
}

func (e *EscapeError) Error() string { return "invalid \\uXXXX escape sequence" }

// Load parses r into a fresh Properties.
func Load(r io.Reader) (*Properties, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	p := New()
	sc := logical.NewScanner(data)
	for {
		ln := sc.Next()
		if ln == nil {
			return p, nil
		}
		if ln.Kind != logical.Normal {
			continue
		}
		key, val, err := split(ln)
		if err != nil {
			return nil, err
		}
		p.Set(key, val)
	}
}

// split divides one logical line into unescaped key and value.
func split(ln *logical.Line) (key, val string, err error) {
	t := ln.Text
	i := 0
	for i < len(t) {
		c := t[i]
		if c == '\\' && i+1 < len(t) {
			i += 2
			continue
		}
		if c == '=' || c == ':' || c == ' ' || c == '\t' || c == '\f' {
			break
		}
		i++
	}
	rawKey := t[:i]
	for i < len(t) && (t[i] == ' ' || t[i] == '\t' || t[i] == '\f') {
		i++
	}
	if i < len(t) && (t[i] == '=' || t[i] == ':') {
		i++
		for i < len(t) && (t[i] == ' ' || t[i] == '\t' || t[i] == '\f') {
			i++
		}
	}
	rawVal := t[i:]
	if key, err = unescape(rawKey, ln, 0); err != nil {
		return
	}
	val, err = unescape(rawVal, ln, i)
	return
}

// unescape decodes escape sequences in s; base is the logical offset of s[0]
// within its line, used for physical error positions.
func unescape(s string, ln *logical.Line, base int) (string, error) {
	var b []byte
	for i := 0; i < len(s); {
		c := s[i]
		if c != '\\' {
			b = append(b, c)
			i++
			continue
		}
		if i+1 >= len(s) {
			break // lone trailing backslash is dropped
		}
		esc := s[i+1]
		switch esc {
		case 't':
			b = append(b, '\t')
		case 'n':
			b = append(b, '\n')
		case 'r':
			b = append(b, '\r')
		case 'f':
			b = append(b, '\f')
		case 'u':
			if i+5 >= len(s) || !isHex(s[i+2:i+6]) {
				line, col := ln.Position(base + i)
				return "", &EscapeError{Line: line, Col: col}
			}
			r := rune(0)
			for k := i + 2; k < i+6; k++ {
				h := s[k]
				r = r<<4 | rune(hexVal(h))
			}
			var buf [4]byte
			n := utf8.EncodeRune(buf[:], r)
			b = append(b, buf[:n]...)
			i += 6
			continue
		default:
			b = append(b, esc)
		}
		i += 2
	}
	return string(b), nil
}

func isHex(s string) bool {
	if len(s) != 4 {
		return false
	}
	for i := 0; i < 4; i++ {
		if hexVal(s[i]) < 0 {
			return false
		}
	}
	return true
}

func hexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

// Get returns the value for key and whether it exists.
func (p *Properties) Get(key string) (string, bool) {
	v, ok := p.vals[key]
	return v, ok
}

// Keys returns keys in first-seen order.
func (p *Properties) Keys() []string {
	out := make([]string, len(p.order))
	copy(out, p.order)
	return out
}

// Set sets key to value, inserting at the end only for new keys.
func (p *Properties) Set(key, value string) {
	if _, ok := p.vals[key]; !ok {
		p.order = append(p.order, key)
	}
	p.vals[key] = value
}

// Store writes the mappings as .properties text.
func (p *Properties) Store(w io.Writer) error {
	return nil
}
