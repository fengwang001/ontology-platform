// Package props implements Java .properties load/store on logical lines.
package props

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"ontology/logical"
)

// EscapeError reports a malformed \uXXXX escape at a physical position.
type EscapeError struct{ Line, Col int }

func (e *EscapeError) Error() string {
	return fmt.Sprintf("props: bad unicode escape at line %d, col %d", e.Line, e.Col)
}

// Entry is one key/value pair.
type Entry struct{ Key, Val string }

// Map keeps pairs in first-occurrence order; later Sets overwrite in place.
type Map struct {
	idx map[string]int
	Ent []Entry
}

// Set inserts or overwrites k, keeping the first-occurrence position.
func (m *Map) Set(k, v string) {
	if m.idx == nil {
		m.idx = map[string]int{}
	}
	if i, ok := m.idx[k]; ok {
		m.Ent[i].Val = v
		return
	}
	m.idx[k] = len(m.Ent)
	m.Ent = append(m.Ent, Entry{k, v})
}

// Load parses .properties data from r.
func Load(r io.Reader) (*Map, error) {
	lines, err := logical.Split(r)
	if err != nil {
		return nil, err
	}
	m := new(Map)
	for _, ln := range lines {
		k, v, err := parse(ln)
		if err != nil {
			return nil, err
		}
		m.Set(k, v)
	}
	return m, nil
}

func parse(ln logical.Line) (string, string, error) {
	s := ln.Text
	end := 0
	for end < len(s) {
		if c := s[end]; c == '\\' {
			end++
		} else if strings.IndexByte(" \t\f", c) >= 0 || c == '=' || c == ':' {
			break
		}
		end++
	}
	key, err := unescape(s[:end], ln, 0)
	if err != nil {
		return "", "", err
	}
	rest := strings.TrimLeft(s[end:], " \t\f")
	if rest != "" && (rest[0] == '=' || rest[0] == ':') {
		rest = strings.TrimLeft(rest[1:], " \t\f")
	}
	val, err := unescape(rest, ln, len(s)-len(rest))
	if err != nil {
		return "", "", err
	}
	return key, val, nil
}

func unescape(s string, ln logical.Line, off int) (string, error) {
	if strings.IndexByte(s, '\\') < 0 {
		return s, nil
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			b.WriteByte(s[i])
			continue
		}
		bs := i
		if i++; i >= len(s) {
			break // lone trailing backslash: dropped
		}
		if c := s[i]; c != 'u' {
			if j := strings.IndexByte("tnrf", c); j >= 0 {
				c = "\t\n\r\f"[j]
			}
			b.WriteByte(c) // \x keeps x unless x is one of tnrf
			continue
		}
		v, err := strconv.ParseUint(s[i+1:min(i+5, len(s))], 16, 32)
		if i+4 >= len(s) || err != nil {
			return "", &EscapeError{ln.Num, ln.Col + off + bs}
		}
		b.WriteRune(rune(v))
		i += 4
	}
	return b.String(), nil
}

// Store writes m so that Load(Store(m)) == m, escaping only what must
// be escaped; non-ASCII bytes pass through as UTF-8.
func (m *Map) Store(w io.Writer) error {
	for _, e := range m.Ent {
		if _, err := io.WriteString(w, esc(e.Key, true)+"="+esc(e.Val, false)+"\n"); err != nil {
			return err
		}
	}
	return nil
}

func esc(s string, key bool) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch j := strings.IndexByte("\\\t\n\r\f", c); {
		case j == 0:
			b.WriteString(`\\`)
		case j > 0:
			b.WriteByte('\\')
			b.WriteByte("tnrf"[j-1])
		default:
			need := i == 0 && c == ' '
			if key {
				need = c == ' ' || c == '=' || c == ':' || i == 0 && (c == '#' || c == '!')
			}
			if need {
				b.WriteByte('\\')
			}
			b.WriteByte(c)
		}
	}
	return b.String()
}
