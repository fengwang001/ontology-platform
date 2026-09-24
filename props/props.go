// Package props splits logical lines into key/value pairs, unescapes
// them (including XXXX), and stores them back.
package props

import (
	"fmt"
	"strconv"
	"strings"

	"ontology/logical"
)

// EscapeError reports a malformed XXXX escape at a physical
// position; detect it with errors.As.
type EscapeError struct{ Row, Col int }

func (e *EscapeError) Error() string {
	return fmt.Sprintf("props: malformed \\uXXXX escape at row %d, col %d", e.Row, e.Col)
}

// Properties is an insertion-ordered string map.
type Properties struct {
	keys []string
	vals map[string]string
}

// New returns an empty Properties.
func New() *Properties { return &Properties{vals: map[string]string{}} }

// Get returns the value stored under key.
func (p *Properties) Get(key string) (string, bool) { v, ok := p.vals[key]; return v, ok }

// Keys returns the keys in first-occurrence order.
func (p *Properties) Keys() []string { return append([]string(nil), p.keys...) }

// Set inserts or replaces key, keeping first-occurrence order.
func (p *Properties) Set(key, val string) {
	if _, ok := p.vals[key]; !ok {
		p.keys = append(p.keys, key)
	}
	p.vals[key] = val
}

func isWS(b byte) bool { return b == ' ' || b == '\t' || b == '\f' }

// Load parses data into ordered properties.
func Load(data []byte) (*Properties, error) {
	p := New()
	for _, ln := range logical.Lines(data) {
		s, i := ln.Text, 0
		for esc := false; i < len(s); i++ { // key ends at unescaped sep/space
			if c := s[i]; esc {
				esc = false
			} else if c == '\\' {
				esc = true
			} else if c == '=' || c == ':' || isWS(c) {
				break
			}
		}
		key, err := unescape(s[:i], ln, 0)
		if err != nil {
			return nil, err
		}
		for i < len(s) && isWS(s[i]) {
			i++
		}
		if i < len(s) && (s[i] == '=' || s[i] == ':') {
			for i++; i < len(s) && isWS(s[i]); i++ {
			}
		}
		val, err := unescape(s[i:], ln, i)
		if err != nil {
			return nil, err
		}
		p.Set(key, val)
	}
	return p, nil
}

func unescape(s string, ln logical.Line, off int) (string, error) {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			b.WriteByte(s[i])
			continue
		}
		if i++; i == len(s) {
			b.WriteByte('\\')
			break
		}
		if j := strings.IndexByte("tnrf", s[i]); j >= 0 {
			b.WriteByte("\t\n\r\f"[j])
			continue
		}
		if s[i] != 'u' { // \x -> x for any other x
			b.WriteByte(s[i])
			continue
		}
		v, err := int64(0), fmt.Errorf("short escape")
		if i+4 < len(s) {
			v, err = strconv.ParseInt(s[i+1:i+5], 16, 32)
		}
		if err != nil {
			row, col := ln.Pos(off + i - 1)
			return "", &EscapeError{row, col}
		}
		b.WriteRune(rune(v))
		i += 4
	}
	return b.String(), nil
}

// Store serializes p so that Load(Store(p)) reproduces p exactly.
func Store(p *Properties) []byte {
	var b strings.Builder
	for _, k := range p.keys {
		b.WriteString(esc(k, true))
		b.WriteByte('=')
		b.WriteString(esc(p.vals[k], false))
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

// esc escapes only what load requires: in keys every whitespace and
// the separators, in values only leading whitespace, everywhere
// backslash and line terminators; a leading '#'/'!' so the line is
// not misread as a comment. Non-ASCII passes through as UTF-8.
func esc(s string, key bool) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c, pre := s[i], false
		switch c {
		case '\\':
			pre = true
		case '\n':
			c, pre = 'n', true
		case '\r':
			c, pre = 'r', true
		case ' ', '\t', '\f':
			pre = key || i == 0
		case '=', ':':
			pre = key
		case '#', '!':
			pre = key && i == 0
		}
		if pre {
			b.WriteByte('\\')
		}
		b.WriteByte(c)
	}
	return b.String()
}
