// Package props parses and writes java.util.Properties compatible text.
package props

import (
	"fmt"
	"io"
	"strconv"
	"unicode/utf8"

	"ontology/logical"
)

// Pair is one key/value entry in first-seen order.
type Pair struct{ Key, Value string }

// SyntaxError reports a malformed escape with physical line/column (1-based).
type SyntaxError struct {
	Line   int
	Column int
	Msg    string
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("properties: %s at line %d column %d", e.Msg, e.Line, e.Column)
}

// Properties keeps entries ordered by first appearance of each key.
type Properties struct {
	order []string
	m     map[string]string
}

// New returns an empty ordered property set.
func New() *Properties { return &Properties{m: map[string]string{}} }

// Len returns the number of distinct keys.
func (p *Properties) Len() int { return len(p.order) }

// Get returns the value for a key.
func (p *Properties) Get(key string) (string, bool) {
	v, ok := p.m[key]
	return v, ok
}

// Pairs returns all entries in first-seen order.
func (p *Properties) Pairs() []Pair {
	out := make([]Pair, 0, len(p.order))
	for _, k := range p.order {
		out = append(out, Pair{k, p.m[k]})
	}
	return out
}

// Set inserts or overwrites a key; order follows the first Set.
func (p *Properties) Set(key, value string) {
	if _, ok := p.m[key]; !ok {
		p.order = append(p.order, key)
	}
	p.m[key] = value
}

// Load reads properties from r following java.util.Properties semantics.
func Load(r io.Reader) (*Properties, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	lr := logical.Reader{}
	lines := lr.Read(data)
	p := New()
	for _, ln := range lines {
		if ln.Blank || ln.Comment {
			continue
		}
		key, value, perr := split(ln)
		if perr != nil {
			return nil, perr
		}
		p.Set(key, value)
	}
	return p, nil
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\f' }

func split(ln logical.Line) (string, string, error) {
	text := ln.Text
	i := 0
	for i < len(text) && isSpace(text[i]) {
		i++
	}
	var key []byte
	for i < len(text) {
		c := text[i]
		if c == '\\' {
			if i == len(text)-1 {
				i++
				continue
			}
			ch, n, err := unescape(text, i, &ln)
			if err != nil {
				return "", "", err
			}
			key = append(key, ch...)
			i += n
			continue
		}
		if c == ' ' || c == '\t' || c == '\f' || c == '=' || c == ':' {
			break
		}
		key = append(key, c)
		i++
	}
	for i < len(text) && isSpace(text[i]) {
		i++
	}
	if i < len(text) && (text[i] == '=' || text[i] == ':') {
		i++
		for i < len(text) && isSpace(text[i]) {
			i++
		}
	}
	var value []byte
	for i < len(text) {
		if text[i] == '\\' {
			ch, n, err := unescape(text, i, &ln)
			if err != nil {
				return "", "", err
			}
			value = append(value, ch...)
			i += n
			continue
		}
		value = append(value, text[i])
		i++
	}
	return string(key), string(value), nil
}

func unescape(text string, i int, ln *logical.Line) ([]byte, int, error) {
	line, col := ln.Position(i)
	if i+1 >= len(text) {
		if i == len(text)-1 && text[i] == '\\' {
			return nil, 1, nil // lone trailing backslash contributes nothing
		}
		return nil, 1, nil
	}
	switch c := text[i+1]; c {
	case 't':
		return []byte{'\t'}, 2, nil
	case 'n':
		return []byte{'\n'}, 2, nil
	case 'r':
		return []byte{'\r'}, 2, nil
	case 'f':
		return []byte{'\f'}, 2, nil
	case 'u':
		if i+6 > len(text) {
			return nil, 0, &SyntaxError{line, col, "malformed \\uxxxx escape"}
		}
		v, err := strconv.ParseUint(text[i+2:i+6], 16, 32)
		if err != nil {
			return nil, 0, &SyntaxError{line, col, "malformed \\uxxxx escape"}
		}
		enc := []byte(string(rune(v)))
		return enc, 6, nil
	default:
		return []byte{c}, 2, nil
	}
}

// Store writes the properties in first-seen order.
func (p *Properties) Store(w io.Writer) error {
	var buf []byte
	for _, k := range p.order {
		buf = appendEscape(buf, k, true)
		buf = append(buf, '=')
		buf = appendEscape(buf, p.m[k], false)
		buf = append(buf, '\n')
	}
	_, err := w.Write(buf)
	return err
}

func appendEscape(buf []byte, s string, isKey bool) []byte {
	for i, r := range s {
		switch {
		case r == '\\':
			buf = append(buf, '\\', '\\')
		case r == '\n':
			buf = append(buf, '\\', 'n')
		case r == '\r':
			buf = append(buf, '\\', 'r')
		case r == '\t':
			buf = append(buf, '\\', 't')
		case r == '\f':
			buf = append(buf, '\\', 'f')
		case isKey && (r == '=' || r == ':' || r == ' ' || r == '\t' || r == '\f'):
			buf = append(buf, '\\')
			buf = appendRune(buf, r)
		case isKey && i == 0 && (r == '#' || r == '!'):
			buf = append(buf, '\\')
			buf = appendRune(buf, r)
		case !isKey && i == 0 && (r == ' ' || r == '\t' || r == '\f'):
			buf = append(buf, '\\')
			buf = appendRune(buf, r)
		default:
			buf = appendRune(buf, r)
		}
	}
	return buf
}

func appendRune(buf []byte, r rune) []byte {
	var tmp [4]byte
	n := utf8.EncodeRune(tmp[:], r)
	return append(buf, tmp[:n]...)
}
