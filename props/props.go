// Package props parses/writes Java .properties (java.util.Properties load/store).
package props

import (
	"errors"
	"io"
	"ontology/logical"
	"strconv"
	"strings"
)

type DecodeError struct {
	Line, Col int
	What      string
}

var ErrBadUnicodeEscape = errors.New("invalid unicode escape: expected four hex digits after \\u")

func (e *DecodeError) Error() string {
	return "properties: " + e.What + " at line " + strconv.Itoa(e.Line) + ", column " + strconv.Itoa(e.Col)
}

type Properties struct {
	order []string
	m     map[string]string
}

func New() *Properties                              { return &Properties{m: map[string]string{}} }
func (p *Properties) Get(key string) (string, bool) { v, ok := p.m[key]; return v, ok }
func (p *Properties) Len() int                      { return len(p.order) }
func (p *Properties) All() []string                 { return append([]string(nil), p.order...) }
func (p *Properties) Set(key, value string) {
	if _, seen := p.m[key]; !seen {
		p.order = append(p.order, key)
	}
	p.m[key] = value
}
func (p *Properties) Load(r io.Reader) error {
	sc := logical.NewScanner(r)
	for {
		ln, err := sc.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		key, value, perr := split(ln)
		if perr != nil {
			return perr
		}
		if _, seen := p.m[key]; !seen {
			p.order = append(p.order, key)
		}
		p.m[key] = value
	}
}
func split(ln *logical.Line) (string, string, error) {
	t, pos, n := ln.Text, ln.Pos, len(ln.Text)
	sep := n
	for i := 0; i < n; i++ {
		if t[i] == '\\' {
			i++
		} else if strings.ContainsRune("=: \t\f", t[i]) {
			sep = i
			break
		}
	}
	key, err := convert(t[:sep], pos[:sep])
	if err != nil {
		return "", "", err
	}
	i := sep + 1
	for i < n && strings.ContainsRune(" \t\f", t[i]) {
		i++
	}
	value, err := convert(t[i:], pos[i:])
	return key, value, err
}
func convert(t []rune, pos []logical.Pos) (string, error) {
	var b strings.Builder
	ctrl := map[rune]rune{'t': '\t', 'n': '\n', 'r': '\r', 'f': '\f'}
	for i := 0; i < len(t); i++ {
		if t[i] != '\\' {
			b.WriteRune(t[i])
			continue
		}
		if i+1 >= len(t) {
			break // lone trailing backslash is dropped
		}
		p, c := pos[i], t[i+1]
		i++
		if c == 'u' {
			if i+4 >= len(t) {
				return "", &DecodeError{p.Line, p.Col, ErrBadUnicodeEscape.Error()}
			}
			v := 0
			for j := 1; j <= 4; j++ {
				h := hexVal(t[i+j])
				if h < 0 {
					return "", &DecodeError{p.Line, p.Col, ErrBadUnicodeEscape.Error()}
				}
				v = v<<4 | h
			}
			b.WriteRune(rune(v))
			i += 4
		} else if ctrl[c] != 0 {
			b.WriteRune(ctrl[c])
		} else {
			b.WriteRune(c) // \x -> x
		}
	}
	return b.String(), nil
}
func hexVal(ch rune) int {
	if ch >= '0' && ch <= '9' {
		return int(ch - '0')
	}
	if ch >= 'a' && ch <= 'f' {
		return int(ch-'a') + 10
	}
	if ch >= 'A' && ch <= 'F' {
		return int(ch-'A') + 10
	}
	return -1
}
func (p *Properties) Store(w io.Writer) error {
	var b strings.Builder
	for _, k := range p.order {
		writeEscaped(&b, k, true)
		b.WriteByte('=')
		writeEscaped(&b, p.m[k], false)
		b.WriteByte('\n')
	}
	_, err := io.WriteString(w, b.String())
	return err
}
func writeEscaped(b *strings.Builder, s string, key bool) {
	esc := map[rune]rune{'\n': 'n', '\r': 'r', '\t': 't', '\f': 'f'}
	for i, r := range s {
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case esc[r] != 0:
			b.WriteByte('\\')
			b.WriteRune(esc[r])
		case key && (r == '=' || r == ':' || r == ' ') ||
			i == 0 && (key && (r == '#' || r == '!') || r == ' ' || r == '\t' || r == '\f'):
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
}
