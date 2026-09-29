// Package lines splits byte strings into logical lines while preserving each
// line's original terminator ("\n", "\r\n", or no terminator on the last
// line), so that Split followed by Join is byte-for-byte identity.
package lines

// Line is one logical line: Text without terminator plus EOL describing it.
type Line struct {
	Text []byte // line content, never includes '\n' or a trailing '\r'
	EOL  EOL
}

// EOL describes a line terminator.
type EOL uint8

const (
	// None means the line is the final line and has no terminator.
	None EOL = iota
	// LF is "\n".
	LF
	// CRLF is "\r\n".
	CRLF
)

// Bytes returns the original bytes of the line.
func (l Line) Bytes() []byte {
	out := make([]byte, 0, len(l.Text)+2)
	out = append(out, l.Text...)
	if l.EOL == LF {
		out = append(out, '\n')
	} else if l.EOL == CRLF {
		out = append(out, '\r', '\n')
	}
	return out
}

// Split divides data into lines. Empty input yields no lines.
func Split(data []byte) []Line {
	if len(data) == 0 {
		return nil
	}
	var out []Line
	start := 0
	for i := 0; i < len(data); i++ {
		if data[i] != '\n' {
			continue
		}
		text := data[start:i]
		eol := LF
		if len(text) > 0 && text[len(text)-1] == '\r' {
			text = text[:len(text)-1]
			eol = CRLF
		}
		out = append(out, Line{Text: append([]byte(nil), text...), EOL: eol})
		start = i + 1
	}
	if start < len(data) {
		out = append(out, Line{Text: append([]byte(nil), data[start:]...), EOL: None})
	}
	return out
}

// SplitString is Split for strings.
func SplitString(s string) []Line { return Split([]byte(s)) }

// Join reconstructs the original bytes.
func Join(ls []Line) []byte {
	n := 0
	for _, l := range ls {
		n += len(l.Text)
		if l.EOL == LF {
			n++
		} else if l.EOL == CRLF {
			n += 2
		}
	}
	out := make([]byte, 0, n)
	for _, l := range ls {
		out = append(out, l.Bytes()...)
	}
	return out
}

// Equal reports whether two lines have identical content and terminator.
func Equal(a, b Line) bool {
	return a.EOL == b.EOL && string(a.Text) == string(b.Text)
}
