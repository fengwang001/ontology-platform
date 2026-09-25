// Package lines splits byte content into lines preserving original line
// terminators ("\n", "\r\n", or none for the final line) and joins them back
// byte-for-byte. It depends on no other package.
package lines

// Line is one line of input. Text excludes the terminator; EOL is the
// original terminator ("\n", "\r\n", or "" for a final unterminated line).
type Line struct {
	Text []byte
	EOL  []byte
}

// Bytes returns the original bytes of the line, terminator included.
func (l Line) Bytes() []byte {
	out := make([]byte, 0, len(l.Text)+len(l.EOL))
	out = append(out, l.Text...)
	out = append(out, l.EOL...)
	return out
}

// HasNL reports whether the line carries a terminator.
func (l Line) HasNL() bool { return len(l.EOL) > 0 }

// Split divides data into lines keeping each original EOL intact.
// Empty input yields zero lines; a trailing terminator is attached to the
// preceding line rather than producing an empty extra line.
func Split(data []byte) []Line {
	ls := make([]Line, 0)
	for len(data) > 0 {
		i := indexByte(data, '\n')
		if i < 0 {
			ls = append(ls, Line{Text: append([]byte(nil), data...)})
			return ls
		}
		text := data[:i]
		eol := data[i : i+1]
		if len(text) > 0 && text[len(text)-1] == '\r' {
			text = text[:len(text)-1]
			eol = data[i-1 : i+1]
		}
		ls = append(ls, Line{Text: append([]byte(nil), text...), EOL: append([]byte(nil), eol...)})
		data = data[i+1:]
	}
	return ls
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}

// Join rebuilds the exact original bytes from the line sequence.
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

// Equal compares the visible text of two lines (terminator-independent).
func Equal(a, b Line) bool {
	return string(a.Text) == string(b.Text)
}
