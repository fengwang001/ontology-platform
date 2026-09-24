// Package lines splits byte strings into lines while preserving every
// original line terminator ("\n", "\r\n", or a missing terminator on the
// final line), so the exact input bytes can always be reconstructed.
package lines

// Line is one logical line. Content excludes the terminator; EOL is the
// original terminator ("\n", "\r\n") or "" when the line is the final line
// and the input did not end with a newline.
type Line struct {
	Content string
	EOL     string
}

// Bytes returns the line exactly as it appeared in the input.
func (l Line) Bytes() string { return l.Content + l.EOL }

// Split cuts data into Lines, preserving terminators. An empty input yields
// no lines; an input ending in "\n" yields a line whose EOL is "\n" rather
// than a trailing empty line.
func Split(data string) []Line {
	if data == "" {
		return nil
	}
	var out []Line
	start := 0
	for i := 0; i < len(data); i++ {
		if data[i] != '\n' {
			continue
		}
		end := i
		eol := "\n"
		if end > start && data[end-1] == '\r' {
			end--
			eol = "\r\n"
		}
		out = append(out, Line{Content: data[start:end], EOL: eol})
		start = i + 1
	}
	if start < len(data) {
		out = append(out, Line{Content: data[start:], EOL: ""})
	}
	return out
}

// Join reconstructs the exact bytes represented by the given lines.
func Join(ls []Line) string {
	var b []byte
	for _, l := range ls {
		b = append(b, l.Bytes()...)
	}
	return string(b)
}

// Equal reports whether two line sequences are byte-identical, including
// terminators.
func Equal(a, b []Line) bool {
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

// Clone returns an independent copy of the line slice.
func Clone(ls []Line) []Line {
	out := make([]Line, len(ls))
	copy(out, ls)
	return out
}
