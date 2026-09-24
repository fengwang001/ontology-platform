// Package props parses logical lines into key/value pairs (unescaping
// included) and stores them back in Java .properties format.
package props

import (
	"fmt"
	"io"
	"strings"

	"ontology/logical"
)

// EscapeError reports a malformed \uXXXX escape sequence.
type EscapeError struct {
	Line, Col int // 1-based physical position of the offending backslash
	Bad       string
}

func (e *EscapeError) Error() string {
	return fmt.Sprintf("props: malformed escape \\u%s at line %d, column %d", e.Bad, e.Line, e.Col)
}

// Map holds key/value pairs ordered by first key insertion.
type Map struct {
	keys []string
	vals map[string]string
}

// New returns an empty Map.
func New() *Map { return &Map{vals: map[string]string{}} }

// Set inserts or overwrites a pair; order follows first insertion.
func (m *Map) Set(k, v string) {
	if _, ok := m.vals[k]; !ok {
		m.keys = append(m.keys, k)
	}
	m.vals[k] = v
}

// Get returns the value for k.
func (m *Map) Get(k string) (string, bool) { v, ok := m.vals[k]; return v, ok }

// Len returns the number of pairs.
func (m *Map) Len() int { return len(m.keys) }

// Key returns the i-th key in insertion order.
func (m *Map) Key(i int) string { return m.keys[i] }

// Load parses src into a Map; later duplicates overwrite earlier ones.
func Load(src io.Reader) (*Map, error) {
	lines, err := logical.Read(src)
	if err != nil {
		return nil, err
	}
	m := New()
	for _, ln := range lines {
		k, v, err := parse(ln)
		if err != nil {
			return nil, err
		}
		m.Set(k, v)
	}
	return m, nil
}

func isWS(c byte) bool { return c == ' ' || c == '\t' || c == '\f' }

func parse(ln logical.Line) (string, string, error) {
	s := ln.Text
	i := 0
	for i < len(s) && s[i] != '=' && s[i] != ':' && !isWS(s[i]) {
		if s[i] == '\\' {
			i++
		}
		i++
	}
	key, err := unescape(ln, 0, i)
	if err != nil {
		return "", "", err
	}
	j := i
	for j < len(s) && isWS(s[j]) {
		j++
	}
	if j < len(s) && (s[j] == '=' || s[j] == ':') {
		j++
	}
	for j < len(s) && isWS(s[j]) {
		j++
	}
	val, err := unescape(ln, j, len(s))
	if err != nil {
		return "", "", err
	}
	return key, val, nil
}

func hex(c byte) int {
	switch {
	case '0' <= c && c <= '9':
		return int(c - '0')
	case 'a' <= c && c <= 'f':
		return int(c-'a') + 10
	case 'A' <= c && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

func unescape(ln logical.Line, from, to int) (string, error) {
	s := ln.Text[from:to]
	if strings.IndexByte(s, '\\') < 0 {
		return s, nil
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		bs := i // backslash position, for error reporting
		i++
		switch s[i] {
		case 't':
			b.WriteByte('\t')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 'f':
			b.WriteByte('\f')
		case 'u':
			v, ok := 0, i+4 < len(s)
			for k := 1; ok && k <= 4; k++ {
				d := hex(s[i+k])
				ok = d >= 0
				v = v*16 + d
			}
			if !ok {
				l, c := physPos(ln, from+bs)
				end := i + 5
				if end > len(s) {
					end = len(s)
				}
				return "", &EscapeError{l, c, s[i-1:end]}
			}
			b.WriteRune(rune(v))
			i += 4
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String(), nil
}

func physPos(ln logical.Line, off int) (line, col int) {
	sg := ln.Segs[0]
	for _, s := range ln.Segs[1:] {
		if s.Off > off {
			break
		}
		sg = s
	}
	return sg.Line, sg.Col + off - sg.Off
}

// Store writes m to w in .properties format, escaping only what Load
// would misread; non-ASCII bytes are emitted as raw UTF-8.
func Store(w io.Writer, m *Map) error {
	for i := 0; i < m.Len(); i++ {
		k := m.Key(i)
		v, _ := m.Get(k)
		if _, err := io.WriteString(w, esc(k, true)+"="+esc(v, false)+"\n"); err != nil {
			return err
		}
	}
	return nil
}

func esc(s string, key bool) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		pre, out := byte(0), c
		switch c {
		case '\\':
			pre = '\\'
		case '\n':
			pre, out = '\\', 'n'
		case '\r':
			pre, out = '\\', 'r'
		case '\t':
			if key || i == 0 {
				pre, out = '\\', 't'
			}
		case '\f':
			if key || i == 0 {
				pre, out = '\\', 'f'
			}
		case ' ':
			if key || i == 0 {
				pre = '\\'
			}
		case '=', ':':
			if key {
				pre = '\\'
			}
		case '#', '!':
			if key && i == 0 {
				pre = '\\'
			}
		}
		if pre != 0 {
			b.WriteByte(pre)
		}
		b.WriteByte(out)
	}
	return b.String()
}
