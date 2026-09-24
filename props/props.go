// Package props parses and writes java.util.Properties text.
package props

import (
	"errors"
	"fmt"
	"io"
	"strconv"

	"ontology/logical"
)

// ErrUnicode marks a malformed \u escape: exactly four hex digits required.
var ErrUnicode = errors.New("props: malformed \\u escape (need exactly 4 hex digits)")

// UnicodeError locates a malformed \u escape in the physical input.
type UnicodeError struct {
	Line   int // 1-based physical line number
	Column int // 1-based physical column of the backslash
}

func (e *UnicodeError) Error() string {
	return fmt.Sprintf("%s at line %d, column %d", ErrUnicode, e.Line, e.Column)
}
func (e *UnicodeError) Is(t error) bool { return t == ErrUnicode }

// Properties is an ordered key/value map; iteration follows first-seen order.
type Properties struct {
	order []string
	m     map[string]string
}

func New() *Properties { return &Properties{m: map[string]string{}} }

func (p *Properties) Get(k string) (string, bool) { v, ok := p.m[k]; return v, ok }
func (p *Properties) Keys() []string              { return append([]string(nil), p.order...) }

// Set inserts or overwrites a key without changing first-seen order.
func (p *Properties) Set(k, v string) {
	if _, ok := p.m[k]; !ok {
		p.order = append(p.order, k)
	}
	p.m[k] = v
}

// Load reads properties text from r.
func Load(r io.Reader) (*Properties, error) {
	p, sc := New(), logical.NewScanner(r)
	for {
		ln := sc.Next()
		if ln == nil {
			return p, nil
		}
		if ln.Kind != logical.Data {
			continue
		}
		k, v, err := parseLine(ln.Segments)
		if err != nil {
			return nil, err
		}
		p.Set(k, v)
	}
}

// Store writes every property as one line, escaping only what is needed.
func (p *Properties) Store(w io.Writer) error {
	var b []byte
	for _, k := range p.order {
		b = appendEsc(b, k, true)
		b = append(b, '=')
		b = appendEsc(b, p.m[k], false)
		b = append(b, '\n')
	}
	_, err := w.Write(b)
	return err
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\f' }

type cursor struct{ segs []logical.Segment; i, j int }

func (c *cursor) adv() {
	for c.i < len(c.segs) && c.j >= len(c.segs[c.i].Text) {
		c.i, c.j = c.i+1, 0
	}
}
func (c *cursor) done() bool { c.adv(); return c.i >= len(c.segs) }

func (c *cursor) peek() byte {
	if c.done() {
		return 0
	}
	return c.segs[c.i].Text[c.j]
}

func (c *cursor) next() byte {
	if c.done() {
		return 0
	}
	b := c.segs[c.i].Text[c.j]
	c.j++
	return b
}

func (c *cursor) pos() (int, int) {
	s := c.segs[c.i]
	return s.Line, s.Lead + c.j // next byte's column minus one == backslash column
}

func parseLine(segs []logical.Segment) (string, string, error) {
	c := cursor{segs: segs}
	var key, val []byte
	for !c.done() {
		if b := c.peek(); isSpace(b) || b == '=' || b == ':' {
			break
		}
		ch, err := c.readToken()
		if err != nil {
			return "", "", err
		}
		key = append(key, ch...)
	}
	for !c.done() && isSpace(c.peek()) {
		c.next()
	}
	if !c.done() && (c.peek() == '=' || c.peek() == ':') {
		c.next()
	}
	for !c.done() && isSpace(c.peek()) {
		c.next()
	}
	for !c.done() {
		ch, err := c.readToken()
		if err != nil {
			return "", "", err
		}
		val = append(val, ch...)
	}
	return string(key), string(val), nil
}

func (c *cursor) readToken() ([]byte, error) {
	if b := c.next(); b != '\\' {
		return []byte{b}, nil
	}
	line, col := c.pos()
	if c.done() {
		return nil, nil // trailing backslash at EOF is dropped
	}
	switch e := c.next(); e {
	case 't':
		return []byte{'\t'}, nil
	case 'n':
		return []byte{'\n'}, nil
	case 'r':
		return []byte{'\r'}, nil
	case 'f':
		return []byte{'\f'}, nil
	case 'u':
		var hex [4]byte
		for k := range hex {
			if c.done() {
				return nil, &UnicodeError{line, col}
			}
			hex[k] = c.next()
		}
		v, err := strconv.ParseUint(string(hex[:]), 16, 32)
		if err != nil {
			return nil, &UnicodeError{line, col}
		}
		return []byte(string(rune(v))), nil
	default:
		return []byte{e}, nil // \x -> x
	}
}

// appendEsc appends s, minimally escaped. key=true escapes every
// separator/space; values only escape their first whitespace run.
func appendEsc(b []byte, s string, key bool) []byte {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case key && (c == ' ' || isSpace(c) || c == '=' || c == ':'):
			b = append(b, '\\', c)
		case !key && i == 0 && isSpace(c):
			b = append(b, '\\', c)
		case key && i == 0 && (c == '#' || c == '!'):
			b = append(b, '\\', c)
		case c == '\\':
			b = append(b, '\\', '\\')
		case c == '\n':
			b = append(b, '\\', 'n')
		case c == '\r':
			b = append(b, '\\', 'r')
		case c == '\t':
			b = append(b, '\\', 't')
		case c == '\f':
			b = append(b, '\\', 'f')
		default:
			b = append(b, c)
		}
}
	return b
}
