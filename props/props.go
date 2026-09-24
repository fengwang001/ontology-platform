// Package props implements Java .properties load/store semantics.
package props

import (
	"io"
	"strconv"
	"strings"

	"ontology/logical"
)

type Pair struct{ Key, Val string }

// Map is an insertion-ordered string map: re-setting a key keeps its position.
type Map struct {
	pairs []Pair
	index map[string]int
}

func New() *Map { return &Map{index: map[string]int{}} }

func (m *Map) Set(key, val string) {
	if i, ok := m.index[key]; ok {
		m.pairs[i].Val = val
		return
	}
	m.index[key] = len(m.pairs)
	m.pairs = append(m.pairs, Pair{key, val})
}

func (m *Map) Len() int { return len(m.pairs) }

func (m *Map) At(i int) Pair { return m.pairs[i] }

// EscapeError reports a malformed \uXXXX escape (1-based physical line/column).
type EscapeError struct{ Line, Col int }

func (e *EscapeError) Error() string {
	return "props: malformed \\u escape at line " + strconv.Itoa(e.Line) + ", column " + strconv.Itoa(e.Col)
}

func Parse(r io.Reader) (*Map, error) {
	lines, err := logical.Lines(r)
	if err != nil {
		return nil, err
	}
	m := New()
	for _, ln := range lines {
		key, val, bad := split(ln.Text)
		if bad >= 0 {
			line, col := ln.Position(bad)
			return nil, &EscapeError{line, col}
		}
		m.Set(key, val)
	}
	return m, nil
}

func isWS(c byte) bool { return c == ' ' || c == '\t' || c == '\f' }

func skipWS(s string, i int) int {
	for i < len(s) && isWS(s[i]) {
		i++
	}
	return i
}

// split divides a logical line into unescaped key and value; bad >= 0 is the offset of a malformed escape.
func split(s string) (key, val string, bad int) {
	i, esc := 0, false
	for i < len(s) && (esc || !isWS(s[i]) && s[i] != '=' && s[i] != ':') {
		esc = !esc && s[i] == '\\'
		i++
	}
	j := skipWS(s, i)
	if j < len(s) && (s[j] == '=' || s[j] == ':') {
		j = skipWS(s, j+1)
	}
	if key, bad = unescape(s[:i], 0); bad >= 0 {
		return "", "", bad
	}
	val, bad = unescape(s[j:], j)
	return key, val, bad
}

// unescape decodes backslash escapes; base is s's offset in the logical line, for reporting a bad \u escape.
func unescape(s string, base int) (string, int) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			b.WriteByte(s[i])
			continue
		}
		if i++; i >= len(s) {
			b.WriteByte('\\')
			break
		}
		switch s[i] {
		case 't', 'n', 'r', 'f':
			b.WriteByte("\t\n\r\f"[strings.IndexByte("tnrf", s[i])])
		case 'u':
			if i+4 >= len(s) {
				return "", base + i - 1
			}
			v, err := strconv.ParseUint(s[i+1:i+5], 16, 32)
			if err != nil {
				return "", base + i - 1
			}
			b.WriteRune(rune(v))
			i += 4
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String(), -1
}

// Store writes m so Parse of the output reproduces it exactly, escaping only what the parser would misread; non-ASCII is UTF-8.
func Store(w io.Writer, m *Map) error {
	var b strings.Builder
	for i := 0; i < m.Len(); i++ {
		p := m.At(i)
		b.WriteString(escape(p.Key, true) + "=" + escape(p.Val, false) + "\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// escape renders s as a key (key=true) or value: keys escape whitespace and = : anywhere plus # ! at
// the start, values escape only leading whitespace; both escape backslash and line breaks.
func escape(s string, key bool) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		default:
			ws := c == ' ' || c == '\t' || c == '\f'
			if ws && (key || i == 0) || key && (c == '=' || c == ':' || i == 0 && (c == '#' || c == '!')) {
				b.WriteByte('\\')
			}
			b.WriteByte(c)
		}
	}
	return b.String()
}
