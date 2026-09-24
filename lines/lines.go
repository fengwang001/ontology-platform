// Package lines splits byte strings into lines while preserving each line's
// original terminator ("\n", "\r\n", or none for the final line).
package lines

// Line is one split line. Text excludes the terminator; NL is "\n" or
// "\r\n"; CR is true only when the terminator is "\r\n". None is true when
// the line is the final input line and has no terminator.
type Line struct {
	Text []byte
	NL   []byte
	CR   bool
	None bool
}

// Bytes returns the line exactly as it appeared in the input.
func (l Line) Bytes() []byte {
	if l.None || len(l.NL) == 0 {
		return l.Text
	}
	out := make([]byte, 0, len(l.Text)+len(l.NL))
	out = append(out, l.Text...)
	out = append(out, l.NL...)
	return out
}

// Split cuts data into Lines. Empty input yields no lines.
func Split(data []byte) []Line {
	var ls []Line
	for len(data) > 0 {
		i := indexByte(data, '\n')
		if i < 0 {
			ls = append(ls, Line{Text: append([]byte(nil), data...), None: true})
			break
		}
		l := Line{Text: append([]byte(nil), data[:i]...), NL: []byte{'\n'}}
		if i > 0 && l.Text[i-1] == '\r' {
			l.Text = l.Text[:i-1]
			l.NL = []byte{'\r', '\n'}
			l.CR = true
		}
		ls = append(ls, l)
		data = data[i+1:]
	}
	return ls
}

// Join rebuilds the exact original byte string from lines.
func Join(ls []Line) []byte {
	n := 0
	for _, l := range ls {
		n += len(l.Text) + len(l.NL)
	}
	out := make([]byte, 0, n)
	for _, l := range ls {
		out = append(out, l.Text...)
		out = append(out, l.NL...)
	}
	return out
}

// Equal reports whether two lines have identical content and terminator.
func Equal(a, b Line) bool {
	return bytesEqual(a.Text, b.Text) && bytesEqual(a.NL, b.NL) && a.None == b.None
}

func indexByte(s []byte, c byte) int {
	for i, b := range s {
		if b == c {
			return i
		}
	}
	return -1
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
