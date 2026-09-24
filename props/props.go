// Package props parses and writes java.util.Properties text on top of the
// logical-line assembly provided by package logical.
package props

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"ontology/logical"
)

// Entry is one key/value pair.
type Entry struct {
	Key   string
	Value string
}

// Map keeps entries in first-occurrence order; a repeated key overwrites the
// value but does not change the position.
type Map struct {
	ents []Entry
	idx  map[string]int
}

// New returns an empty Map.
func New() *Map { return &Map{idx: map[string]int{}} }

// Set inserts or overwrites a key.
func (m *Map) Set(k, v string) {
	if i, ok := m.idx[k]; ok {
		m.ents[i].Value = v
		return
	}
	m.idx[k] = len(m.ents)
	m.ents = append(m.ents, Entry{k, v})
}

// Get returns the value of a key.
func (m *Map) Get(k string) (string, bool) {
	if i, ok := m.idx[k]; ok {
		return m.ents[i].Value, true
	}
	return "", false
}

// Len is the number of distinct keys.
func (m *Map) Len() int { return len(m.ents) }

// Entries returns the entries in first-occurrence order.
func (m *Map) Entries() []Entry { return m.ents }

// SyntaxError reports a malformed escape with physical line and column.
type SyntaxError struct {
	Line int
	Col  int
	Msg  string
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("properties: line %d, column %d: %s", e.Line, e.Col, e.Msg)
}

// Load parses properties text from r.
func Load(r io.Reader) (*Map, error) {
	lines, err := logical.Split(r)
	if err != nil {
		return nil, err
	}
	m := New()
	for _, l := range lines {
		if l.Kind != logical.Entry {
			continue
		}
		k, v, err := parseLine(l)
		if err != nil {
			return nil, err
		}
		m.Set(k, v)
	}
	return m, nil
}

func parseLine(l logical.Line) (string, string, error) {
	s := l.Text
	prec := false
	i := 0
	for ; i < len(s); i++ {
		c := s[i]
		if !prec && (c == '=' || c == ':') {
			k, err := decode(l, 0, s[:i])
			if err != nil {
				return "", "", err
			}
			v, err := decode(l, i+1, s[skipSep(s, i+1, true):])
			if err != nil {
				return "", "", err
			}
			return k, v, nil
		}
		if !prec && (c == ' ' || c == '\t' || c == '\f') {
			k, err := decode(l, 0, s[:i])
			if err != nil {
				return "", "", err
			}
			j := skipSep(s, i+1, false)
			v, err := decode(l, i+1, s[j:])
			if err != nil {
				return "", "", err
			}
			return k, v, nil
		}
		if c == '\\' {
			prec = !prec
		} else {
			prec = false
		}
	}
	k, err := decode(l, 0, s)
	return k, "", err
}

func skipSep(s string, i int, hadSep bool) int {
	for i < len(s) {
		c := s[i]
		if c == ' ' || c == '\t' || c == '\f' {
			i++
			continue
		}
		if !hadSep && (c == '=' || c == ':') {
			hadSep = true
			i++
			continue
		}
		break
	}
	return i
}

func decode(l logical.Line, base int, raw string) (string, error) {
	return unescape(raw, func(off int) (int, int) { return l.Pos(base + off) })
}

func unescape(raw string, at func(off int) (int, int)) (string, error) {
	var b strings.Builder
	b.Grow(len(raw))
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		if i+1 >= len(raw) {
			b.WriteByte('\\')
			continue
		}
		i++
		switch raw[i] {
		case 't':
			b.WriteByte('\t')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 'f':
			b.WriteByte('\f')
		case 'u':
			if i+4 >= len(raw) {
				line, col := at(i)
				return "", &SyntaxError{line, col, "malformed \\uxxxx encoding"}
			}
			var r rune
			for j := 1; j <= 4; j++ {
				h, ok := hexDigit(raw[i+j])
				if !ok {
					line, col := at(i)
					return "", &SyntaxError{line, col, "malformed \\uxxxx encoding"}
				}
				r = r<<4 | rune(h)
			}
			b.WriteRune(r)
			i += 4
		default:
			b.WriteByte(raw[i])
		}
	}
	return b.String(), nil
}

func hexDigit(c byte) (int, bool) {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0'), true
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10, true
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10, true
	}
	return 0, false
}

// Store writes the map to w in first-occurrence order.
func (m *Map) Store(w io.Writer) error {
	bw := bufio.NewWriter(w)
	for _, e := range m.ents {
		if _, err := bw.WriteString(escKey(e.Key)); err != nil {
			return err
		}
		if err := bw.WriteByte('='); err != nil {
			return err
		}
		if _, err := bw.WriteString(escVal(e.Value)); err != nil {
			return err
		}
		if err := bw.WriteByte('\n'); err != nil {
			return err
		}
	}
	return bw.Flush()
}

func escWS(c byte) byte {
	if c == '\t' {
		return 't'
	}
	if c == '\f' {
		return 'f'
}
	return ' '
}

func escKey(s string) string {
	b := &strings.Builder{}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 0x80:
			b.WriteByte(c)
		case c == '\\':
			b.WriteString(`\\`)
		case c == ' ', c == '\t', c == '\f', c == '\n', c == '\r':
			b.WriteByte('\\')
			if c == '\n' {
				b.WriteByte('n')
			} else if c == '\r' {
				b.WriteByte('r')
			} else {
				b.WriteByte(escWS(c))
			}
		case c == '=' || c == ':':
			b.WriteByte('\\')
			b.WriteByte(c)
		case (c == '#' || c == '!') && i == 0:
			b.WriteByte('\\')
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func escVal(s string) string {
	b := &strings.Builder{}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 0x80:
			b.WriteByte(c)
		case c == '\\':
			b.WriteString(`\\`)
		case c == '\n', c == '\r':
			b.WriteByte('\\')
			if c == '\n' {
				b.WriteByte('n')
			} else {
				b.WriteByte('r')
			}
		case i == 0 && (c == ' ' || c == '\t' || c == '\f'):
			b.WriteByte('\\')
			b.WriteByte(escWS(c))
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}
