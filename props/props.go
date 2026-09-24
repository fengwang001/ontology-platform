// Package props parses and writes Java .properties logical lines.
package props

import (
	"bytes"
	"fmt"
	"io"

	"ontology/logical"
)

// DecodeError reports an invalid escape with physical line/column (1-based).
type DecodeError struct {
	Line int
	Col  int
	Msg  string
}

func (e *DecodeError) Error() string {
	return fmt.Sprintf("properties: %s at line %d column %d", e.Msg, e.Line, e.Col)
}

// Props is an insertion-ordered key/value map; duplicate keys keep first position.
type Props struct {
	keys   []string
	values map[string]string
}

// New returns an empty property set.
func New() *Props { return &Props{values: map[string]string{}} }

// Set inserts or updates a key, preserving first-appearance position.
func (p *Props) Set(key, value string) {
	if _, ok := p.values[key]; !ok {
		p.keys = append(p.keys, key)
	}
	p.values[key] = value
}

// Load parses a properties stream in first-appearance order.
func (p *Props) Load(r io.Reader) error {
	lr, err := logical.NewReader(r)
	if err != nil {
		return err
	}
	for {
		ln := lr.Next()
		if ln == nil {
			return nil
		}
		if ln.Kind != logical.Data {
			continue
		}
		key, val, derr := decode(lr, ln.Segs)
		if err != nil {
			return derr
		}
		if _, ok := p.values[key]; !ok {
			p.keys = append(p.keys, key)
		}
		p.values[key] = val
	}
}

// Get returns the value of a key.
func (p *Props) Get(key string) (string, bool) {
	v, ok := p.values[key]
	return v, ok
}

// Ordered returns keys in first-appearance order.
func (p *Props) Ordered() []string { return p.keys }

// Len returns the number of distinct keys.
func (p *Props) Len() int { return len(p.keys) }

// Store writes the properties back in .properties format.
func (p *Props) Store(w io.Writer) error {
	var b bytes.Buffer
	for _, k := range p.keys {
		b.WriteString(escape(k, true))
		b.WriteByte('=')
		b.WriteString(escape(p.values[k], false))
		b.WriteByte('\n')
	}
	_, err := w.Write(b.Bytes())
	return err
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\f' }

// mapped holds the concatenated logical text and the absolute input
// offset of every logical byte, for physical line/column reporting.
type mapped struct {
	raw []byte
	off []int
}

func buildMapped(segs []logical.Seg) mapped {
	n := 0
	for _, s := range segs {
		n += len(s.Text)
	}
	m := mapped{raw: make([]byte, 0, n), off: make([]int, 0, n)}
	for _, s := range segs {
		for i := 0; i < len(s.Text); i++ {
			m.raw = append(m.raw, s.Text[i])
			m.off = append(m.off, s.Offset+i)
		}
	}
	return m
}

func decode(lr *logical.Reader, segs []logical.Seg) (string, string, error) {
	m := buildMapped(segs)
	raw := m.raw
	fail := func(i int, msg string) error {
		line, col := lr.Location(m.off[i])
		return &DecodeError{Line: line, Col: col, Msg: msg}
	}
	i := 0
	for i < len(raw) && isSpace(raw[i]) {
		i++
	}
	keyStart := i
	for i < len(raw) {
		b := raw[i]
		if b == '\\' {
			i++
			if i < len(raw) && raw[i] == 'u' {
				n, ok := unicodeLen(raw[i:])
				if !ok {
					return "", "", fail(i-1, "invalid \\uXXXX escape")
				}
				i += n
			} else {
				i++
			}
			continue
		}
		if b == '=' || b == ':' || isSpace(b) {
			break
		}
		i++
	}
	keyEnd := i
	for i < len(raw) && isSpace(raw[i]) {
		i++
	}
	if i < len(raw) && (raw[i] == '=' || raw[i] == ':') {
		i++
		for i < len(raw) && isSpace(raw[i]) {
			i++
		}
	}
	valStart := i
	key, err := unescape(lr, m, keyStart, keyEnd)
	if err != nil {
		return "", "", err
	}
	val, err := unescape(lr, m, valStart, len(raw))
	if err != nil {
		return "", "", err
	}
	return key, val, nil
}

// unicodeLen reports n = 1 + (#u) + 4 when a valid \\uXXXX follows;
// multiple leading u are accepted as in java.util.Properties.
func unicodeLen(s []byte) (int, bool) {
	n := 1
	for n < len(s) && s[n] == 'u' {
		n++
	}
	if n+4 > len(s) {
		return 0, false
	}
	for j := n; j < n+4; j++ {
		c := s[j]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return 0, false
		}
	}
	return n + 4, true
}

var hex = [256]rune{}

func init() {
	const digits = "0123456789abcdef"
	for i := 0; i < 256; i++ {
		hex[i] = -1
	}
	for i, d := range digits {
		hex[byte(d)] = rune(i)
		hex[byte(d-('a'-'A'))] = rune(i)
	}
}

func unescape(lr *logical.Reader, m mapped, start, end int) (string, error) {
	var out bytes.Buffer
	for i := start; i < end; {
		b := m.raw[i]
		if b != '\\' {
			out.WriteByte(b)
			i++
			continue
		}
		if i+1 >= end {
			break // lone trailing backslash at logical EOF is dropped
		}
		c := m.raw[i+1]
		if c == 'u' {
			n, ok := unicodeLen(m.raw[i:])
			if !ok {
				line, col := lr.Location(m.off[i])
				return "", &DecodeError{Line: line, Col: col, Msg: "invalid \\uXXXX escape"}
			}
			var r rune
			for j := n - 4; j < n; j++ {
				hb := m.raw[i+j]
				r = r<<4 | hex[hb]
			}
			out.WriteRune(r)
			i += n
			continue
		}
		switch c {
		case 't':
			out.WriteByte('\t')
		case 'n':
			out.WriteByte('\n')
		case 'r':
			out.WriteByte('\r')
		case 'f':
			out.WriteByte('\f')
		default:
			out.WriteByte(c)
		}
		i += 2
	}
	return out.String(), nil
}

func escape(s string, key bool) string {
	var b bytes.Buffer
	for i := 0; i < len(s); i++ {
		c := s[i]
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
		case key && (c == '=' || c == ':'):
			b.WriteByte('\\')
			b.WriteByte(c)
		case key && isSpace(c):
			b.WriteByte('\\')
			b.WriteByte(c)
		case !key && i == 0 && isSpace(c):
			b.WriteByte('\\')
			b.WriteByte(c)
		case (key && i == 0) && (c == '#' || c == '!'):
			b.WriteByte('\\')
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}
