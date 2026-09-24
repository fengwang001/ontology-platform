package props

import (
	"bytes"
	"io"
	"strconv"
	"unicode/utf8"

	"ontology/logical"
)

// UnicodeError is a malformed \uXXXX escape; Line/Column are the 1-based
// physical position of the backslash.
type UnicodeError struct{ Line, Column int }

func (e *UnicodeError) Error() string { return "properties: malformed \\uXXXX escape" }

// Properties is an ordered map; duplicate keys keep first-seen order.
type Properties struct {
	order []string
	m     map[string]string
}

func New() *Properties { return &Properties{m: map[string]string{}} }

func isWS(c byte) bool { return c == ' ' || c == '\t' || c == '\f' }

// Load parses properties text following java.util.Properties semantics.
func (p *Properties) Load(r io.Reader) error {
	sc := logical.NewScanner(r)
	for {
		ln, err := sc.Next()
		if err != nil {
			return err
		}
		if ln == nil {
			return nil
		}
		if ln.IsComment() {
			continue
		}
		b := ln.Text()
		ke, vs := split(b)
		k, err := decode(b[:ke], ln, 0)
		if err != nil {
			return err
		}
		v, err := decode(b[vs:], ln, vs)
		if err != nil {
			return err
		}
		p.Set(k, v)
	}
}

// split returns key end and value start offsets of one logical line.
func split(b []byte) (ke, vs int) {
	for vs < len(b) && !isWS(b[vs]) && b[vs] != '=' && b[vs] != ':' {
		vs++
	}
	ke = vs
	for vs < len(b) && isWS(b[vs]) {
		vs++
	}
	if vs < len(b) && (b[vs] == '=' || b[vs] == ':') {
		vs++
	}
	for vs < len(b) && isWS(b[vs]) {
		vs++
	}
	return ke, vs
}

// decode unescapes raw bytes; base is the slice offset in the logical line.
func decode(b []byte, ln *logical.Line, base int) (string, error) {
	var out bytes.Buffer
	var high rune
	flush := func() {
		if high != 0 {
			out.WriteRune(high)
			high = 0
		}
	}
	for i := 0; i < len(b); i++ {
		if b[i] != '\\' {
			flush()
			out.WriteByte(b[i])
			continue
		}
		if i+1 == len(b) {
			break // lone trailing backslash is dropped
		}
		i++
		if b[i] == 'u' {
			if i+4 >= len(b) || !isX4(b[i+1:i+5]) {
				l, c := ln.PhysicalPos(base + i - 1)
				return "", &UnicodeError{Line: l, Column: c}
			}
			n, _ := strconv.ParseUint(string(b[i+1:i+5]), 16, 32)
			r := rune(n)
			i += 4
			switch {
			case high != 0 && r >= 0xDC00 && r <= 0xDFFF:
				out.WriteRune(0x10000 + (high-0xD800)<<10 + (r - 0xDC00))
				high = 0
			case r >= 0xD800 && r <= 0xDBFF:
				flush()
				high = r
			default:
				flush()
				out.WriteRune(r)
			}
			continue
		}
		flush()
		switch b[i] {
		case 't':
			out.WriteByte('\t')
		case 'n':
			out.WriteByte('\n')
		case 'r':
			out.WriteByte('\r')
		case 'f':
			out.WriteByte('\f')
		default:
			out.WriteByte(b[i])
		}
	}
	flush()
	return out.String(), nil
}

func isX4(b []byte) bool {
	for _, c := range b {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// Set inserts or updates a key preserving first-occurrence order.
func (p *Properties) Set(key, value string) {
	if _, ok := p.m[key]; !ok {
		p.order = append(p.order, key)
	}
	p.m[key] = value
}

func (p *Properties) Get(key string) (string, bool) { v, ok := p.m[key]; return v, ok }
func (p *Properties) Keys() []string                { return append([]string(nil), p.order...) }

// Store writes key=value lines, escaping only what Load requires.
func (p *Properties) Store(w io.Writer) error {
	var b bytes.Buffer
	for _, k := range p.order {
		escape(&b, k, true)
		b.WriteByte('=')
		escape(&b, p.m[k], false)
		b.WriteByte('\n')
	}
	_, err := w.Write(b.Bytes())
	return err
}

// escape writes s; isKey enables separator/comment escaping.
func escape(b *bytes.Buffer, s string, isKey bool) {
	for i, r := range s {
		switch {
		case isKey && (r == '=' || r == ':' || r == ' ' || r == '\t' || r == '\f' ||
			(i == 0 && (r == '#' || r == '!'))):
			b.WriteByte('\\')
			b.WriteRune(r)
		case !isKey && i == 0 && (r == ' ' || r == '\t' || r == '\f'):
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\f':
			b.WriteString(`\f`)
		default:
			var t [4]byte
			b.Write(t[:utf8.EncodeRune(t[:], r)])
		}
	}
}
