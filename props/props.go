// Package props parses and writes Java .properties text on top of logical
// lines provided by package logical.
package props

import (
	"errors"
	"iter"
	"strconv"
	"strings"
	"unicode/utf8"

	"ontology/logical"
)

// ErrInvalidUnicode marks a \\u escape that is not followed by four hex digits.
var ErrInvalidUnicode = errors.New("invalid \\u escape: four hexadecimal digits required")

// PositionError attaches a physical 1-based line and rune column to an error.
type PositionError struct {
	Line int
	Col  int
	Err  error
}

func (e *PositionError) Error() string {
	return e.Err.Error() + " (line " + strconv.Itoa(e.Line) + ", column " + strconv.Itoa(e.Col) + ")"
}

func (e *PositionError) Unwrap() error { return e.Err }

// Properties is an insertion-ordered key/value map; later duplicates win.
type Properties struct {
	order []string
	vals  map[string]string
}

// New returns an empty Properties.
func New() *Properties { return &Properties{vals: map[string]string{}} }

// Set inserts or overwrites a key; first-seen position is kept.
func (p *Properties) Set(key, value string) {
	if _, ok := p.vals[key]; !ok {
		p.order = append(p.order, key)
	}
	p.vals[key] = value
}

// Get returns the value for key.
func (p *Properties) Get(key string) (string, bool) {
	v, ok := p.vals[key]
	return v, ok
}

// Len is the number of distinct keys.
func (p *Properties) Len() int { return len(p.order) }

// All iterates keys and values in first-seen order.
func (p *Properties) All() iter.Seq2[string, string] {
	return func(yield func(string, string) bool) {
		for _, k := range p.order {
			if !yield(k, p.vals[k]) {
				return
			}
		}
	}
}

// Load parses properties text.
func Load(src string) (*Properties, error) {
	p := New()
	sc := logical.NewScanner(src)
	for {
		ln, ok := sc.Next()
		if !ok {
			break
		}
		if ln.Kind() != logical.Data {
			continue
		}
		if err := p.parseLine(ln); err != nil {
			return nil, err
		}
	}
	return p, nil
}

func (p *Properties) parseLine(ln logical.Line) error {
	text := ln.Text()
	i := skipSpace(text, 0)
	var key strings.Builder
	for i < len(text) {
		c := text[i]
		if c == '\\' {
			r, ni, err := readEscape(text, i, ln)
			if err != nil {
				return err
			}
			key.WriteRune(r)
			i = ni
			continue
		}
		if c == '=' || c == ':' || isSpace(c) {
			break
		}
		key.WriteByte(c)
		i++
	}
	i = skipSpace(text, i)
	if i < len(text) && (text[i] == '=' || text[i] == ':') {
		i = skipSpace(text, i+1)
	}
	var val strings.Builder
	for i < len(text) {
		if text[i] == '\\' {
			r, ni, err := readEscape(text, i, ln)
			if err != nil {
				return err
			}
			val.WriteRune(r)
			i = ni
			continue
		}
		val.WriteByte(text[i])
		i++
	}
	p.Set(key.String(), val.String())
	return nil
}

func readEscape(s string, i int, ln logical.Line) (rune, int, error) {
	if i+1 >= len(s) {
		return '\\', i + 1, nil
	}
	c := s[i+1]
	switch c {
	case 't':
		return '\t', i + 2, nil
	case 'n':
		return '\n', i + 2, nil
	case 'r':
		return '\r', i + 2, nil
	case 'f':
		return '\f', i + 2, nil
	case 'u':
		if i+6 > len(s) {
			return 0, i, unicodeErr(ln, i)
		}
		n, err := strconv.ParseUint(s[i+2:i+6], 16, 32)
		if err != nil {
			return 0, i, unicodeErr(ln, i)
		}
		return rune(n), i + 6, nil
	default:
		return rune(c), i + 2, nil
	}
}

func unicodeErr(ln logical.Line, offset int) error {
	line, col := ln.Position(offset)
	return &PositionError{Line: line, Col: col, Err: ErrInvalidUnicode}
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\f' }

func skipSpace(s string, i int) int {
	for i < len(s) && isSpace(s[i]) {
		i++
	}
	return i
}

// Store writes the properties; Load(Store(p)) round-trips exactly.
func Store(p *Properties) string {
	var b strings.Builder
	for k, v := range p.All() {
		escapeInto(&b, k, true)
		b.WriteByte('=')
		escapeInto(&b, v, false)
		b.WriteByte('\n')
	}
	return b.String()
}

func escapeInto(b *strings.Builder, s string, key bool) {
	for i := 0; i < len(s); {
		c := s[i]
		if c >= 0x80 {
			r, size := utf8.DecodeRuneInString(s[i:])
			b.WriteRune(r)
			i += size
			continue
		}
		switch {
		case c == '\\':
			b.WriteString(`\\`)
		case c == '\n':
			b.WriteString(`\n`)
		case c == '\r':
			b.WriteString(`\r`)
		case c == '\t':
			b.WriteString(`\t`)
		case c == '\f':
			b.WriteString(`\f`)
		case key && isSpace(c):
			b.WriteByte('\\')
			b.WriteByte(c)
		case key && (c == '=' || c == ':'):
			b.WriteByte('\\')
			b.WriteByte(c)
		case (key || i == 0) && (c == '#' || c == '!'):
			b.WriteByte('\\')
			b.WriteByte(c)
		case !key && i == 0 && isSpace(c):
			b.WriteByte('\\')
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
		i++
	}
}
