// Package lines splits byte strings into logical lines while preserving each
// original line ending ("\n", "\r\n", or a final line without one) and joins
// them back byte-for-byte.
package lines

// Line is one logical line. Text excludes the terminator; EOL is the exact
// terminator bytes ("\n" or "\r\n"), empty for a final unterminated line.
type Line struct {
	Text []byte
	EOL  []byte
}

// Bytes returns the exact bytes of the line including its terminator.
func (l Line) Bytes() []byte {
	out := make([]byte, 0, len(l.Text)+len(l.EOL))
	out = append(out, l.Text...)
	out = append(out, l.EOL...)
	return out
}

// Split divides data into Lines. Empty input yields no lines.
func Split(data []byte) []Line {
	var ls []Line
	for len(data) > 0 {
		i := indexByte(data, '\n')
		if i < 0 {
			ls = append(ls, Line{Text: append([]byte(nil), data...)})
			break
		}
		text := data[:i]
		eol := data[i : i+1]
		if i > 0 && text[i-1] == '\r' {
			text = text[:i-1]
			eol = data[i-1 : i+1]
		}
		ls = append(ls, Line{Text: append([]byte(nil), text...), EOL: append([]byte(nil), eol...)})
		data = data[i+1:]
	}
	return ls
}

// Join rebuilds the exact original bytes from lines.
func Join(ls []Line) []byte {
	n := 0
	for _, l := range ls {
		n += len(l.Text) + len(l.EOL)
	}
	out := make([]byte, 0, n)
	for _, l := range ls {
		out = append(out, l.Text...)
		out = append(out, l.EOL...)
	}
	return out
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}
