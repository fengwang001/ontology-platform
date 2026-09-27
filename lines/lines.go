// Package lines splits byte content into lines preserving the original line
// endings ("\n", "\r\n", or a missing ending on the final line) and joins them
// back byte-for-byte.
package lines

// Line is one logical line. Text excludes the line terminator; NL is the exact
// terminator ("\n" or "\r\n"), empty when the line is the last and unterminated.
type Line struct {
	Text []byte
	NL   []byte
}

// Split cuts data into lines without copying or normalizing line endings.
// Empty data yields no lines; "a\n" yields one line, "a" yields one line
// without terminator, "" yields none.
func Split(data []byte) []Line {
	ls := []Line{}
	for len(data) > 0 {
		i := indexByte(data, '\n')
		if i < 0 {
			ls = append(ls, Line{Text: data})
			return ls
		}
		text, nl := data[:i], data[i:i+1]
		if i > 0 && text[i-1] == '\r' {
			text, nl = data[:i-1], data[i-1:i+1]
		}
		ls = append(ls, Line{Text: text, NL: nl})
		data = data[i+1:]
	}
	return ls
}

// Join reconstructs the exact bytes passed to Split.
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

// Equal reports line content equality (terminators excluded).
func Equal(x, y Line) bool { return string(x.Text) == string(y.Text) }

// Body returns the one-char payload plus content of a unified-diff body line:
// prefix is ' ', '-', '+', or '\\' (no-newline marker); ok is false otherwise.
func Body(raw []byte) (prefix byte, text []byte, ok bool) {
	if len(raw) == 0 {
		return 0, nil, false
	}
	switch raw[0] {
	case ' ', '-', '+':
		return raw[0], raw[1:], true
	case '\\':
		return '\\', raw, true
	}
	return 0, nil, false
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}
