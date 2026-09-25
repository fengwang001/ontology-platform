// Package lines splits byte strings into lines while preserving every original
// line terminator (\n, \r\n, or a final line without a terminator), and joins
// them back byte-for-byte.
package lines

// Line is one split line. Raw is the original bytes including its terminator.
// Content is Raw without the terminator. NL is "\n", "\r\n", or "" for a final
// unterminated line.
type Line struct {
	Raw     []byte
	Content []byte
	NL      string
}

// Split divides s into lines. An empty input yields zero lines.
func Split(s []byte) []Line {
	ls := make([]Line, 0, 0)
	for len(s) > 0 {
		i := indexByte(s, '\n')
		var raw []byte
		if i < 0 {
			raw = s
			s = nil
		} else {
			raw = s[:i+1]
			s = s[i+1:]
		}
		nl := ""
		content := raw
		if len(raw) > 0 && raw[len(raw)-1] == '\n' {
			nl = "\n"
			content = raw[:len(raw)-1]
			if len(content) > 0 && content[len(content)-1] == '\r' {
				nl = "\r\n"
				content = content[:len(content)-1]
			}
		}
		ls = append(ls, Line{Raw: append([]byte(nil), raw...), Content: append([]byte(nil), content...), NL: nl})
	}
	return ls
}

// Join reconstructs the exact byte string represented by ls.
func Join(ls []Line) []byte {
	n := 0
	for _, l := range ls {
		n += len(l.Raw)
	}
	out := make([]byte, 0, n)
	for _, l := range ls {
		out = append(out, l.Raw...)
	}
	return out
}

// WithTerm returns the line content terminated by the supplied terminator.
func WithTerm(content []byte, nl string) []byte {
	out := make([]byte, 0, len(content)+len(nl))
	out = append(out, content...)
	out = append(out, nl...)
	return out
}

// Clone makes an independent copy of a line.
func (l Line) Clone() Line {
	return Line{
		Raw:     append([]byte(nil), l.Raw...),
		Content: append([]byte(nil), l.Content...),
		NL:      l.NL,
	}
}

func indexByte(s []byte, c byte) int {
	for i, b := range s {
		if b == c {
			return i
		}
	}
	return -1
}
