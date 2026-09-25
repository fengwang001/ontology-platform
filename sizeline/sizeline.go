package sizeline

import "strconv"

// Ext is one chunk extension pair on a size line.
type Ext struct {
	Key   string
	Value string
}

// NeedsQuote reports whether v must be rendered as a quoted string so that
// it cannot change the parse structure of the size line.
func NeedsQuote(v string) bool {
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c == ';' || c == '=' || c == '"' || c == '\\' || c <= 0x20 || c == 0x7f {
			return true
		}
	}
	return false
}

// Value renders one extension value, quoting and escaping it when needed.
func Value(v string) string {
	if !NeedsQuote(v) {
		return v
	}
	b := make([]byte, 0, len(v)+2)
	b = append(b, '"')
	for i := 0; i < len(v); i++ {
		if c := v[i]; c == '\\' || c == '"' {
			b = append(b, '\\', c)
		} else {
			b = append(b, c)
		}
	}
	b = append(b, '"')
	return string(b)
}

// ExtsLen returns the encoded length of the extension part (";k=v;...").
func ExtsLen(exts []Ext) int {
	n := 0
	for _, e := range exts {
		n += 1 + len(e.Key) + 1 + len(Value(e.Value))
	}
	return n
}

// AppendLine appends a complete size line (including trailing CRLF) to dst.
func AppendLine(dst []byte, size int64, exts []Ext) []byte {
	dst = strconv.AppendInt(dst, size, 16)
	for _, e := range exts {
		dst = append(dst, ';')
		dst = append(dst, e.Key...)
		dst = append(dst, '=')
		dst = append(dst, Value(e.Value)...)
	}
	dst = append(dst, '\r', '\n')
	return dst
}

// Line returns a freshly allocated size line.
func Line(size int64, exts []Ext) []byte {
	return AppendLine(make([]byte, 0, 16+ExtsLen(exts)), size, exts)
}
