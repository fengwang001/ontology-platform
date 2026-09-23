// Package lines splits byte strings into lines preserving original line
// terminators (\n, \r\n, or none for the final line) and joins them back
// byte-for-byte.
package lines

// Line is one line of text. Raw includes the original terminator if any.
type Line struct {
	Raw       []byte
	NoNewline bool // true when this is the final line without a terminator
}

// Body returns the line content without its line terminator.
func (l Line) Body() []byte {
	if l.NoNewline {
		return l.Raw
	}
	if len(l.Raw) >= 2 && l.Raw[len(l.Raw)-2] == '\r' {
		return l.Raw[:len(l.Raw)-2]
	}
	return l.Raw[:len(l.Raw)-1]
}

// Split cuts data into lines, preserving terminators. An empty input yields no
// lines; a lone "\n" yields one empty line with a terminator.
func Split(data []byte) []Line {
	if len(data) == 0 {
		return nil
	}
	out := make([]Line, 0, 1)
	start := 0
	for i := 0; i < len(data); i++ {
		if data[i] != '\n' {
			continue
		}
		out = append(out, Line{Raw: data[start : i+1 : i+1]})
		start = i + 1
	}
	if start < len(data) {
		out = append(out, Line{Raw: data[start:len(data):len(data)], NoNewline: true})
	}
	return out
}

// Join concatenates lines back into the original byte string.
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

// Equal reports whether two lines are identical including terminator style.
func Equal(a, b Line) bool {
	if a.NoNewline != b.NoNewline {
		return false
	}
	if len(a.Raw) != len(b.Raw) {
		return false
	}
	for i := range a.Raw {
		if a.Raw[i] != b.Raw[i] {
			return false
		}
	}
	return true
}
