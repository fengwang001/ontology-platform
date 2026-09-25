// Package lines splits byte strings into lines while preserving the original
// line terminator (\n, \r\n, or none for the final line) and joins them back
// byte-for-byte. It depends on no other package.
package lines

// Line is one textual line: Text without its terminator plus EOL, which is
// "\n", "\r\n", or "" for a final line missing a newline.
type Line struct {
	Text string
	EOL  string
}

// Bytes returns the full line including its terminator.
func (l Line) Bytes() string { return l.Text + l.EOL }

// Split cuts s into Lines. An empty input yields no lines. Every byte of s
// belongs to exactly one line, so Join(Split(s)) == s always.
func Split(s string) []Line {
	ls := make([]Line, 0, countLines(s))
	for len(s) > 0 {
		i := indexByte(s, '\n')
		if i < 0 {
			ls = append(ls, Line{Text: s, EOL: ""})
			break
		}
		text := s[:i]
		eol := "\n"
		if len(text) > 0 && text[len(text)-1] == '\r' {
			text = text[:len(text)-1]
			eol = "\r\n"
		}
		ls = append(ls, Line{Text: text, EOL: eol})
		s = s[i+1:]
	}
	return ls
}

// Join concatenates lines exactly as they were split.
func Join(ls []Line) string {
	n := 0
	for _, l := range ls {
		n += len(l.Text) + len(l.EOL)
	}
	b := make([]byte, 0, n)
	for _, l := range ls {
		b = append(b, l.Text...)
		b = append(b, l.EOL...)
	}
	return string(b)
}

// Equal reports whether two line sequences are identical (text and EOL).
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

func countLines(s string) int {
	n := 0
	for len(s) > 0 {
		n++
		i := indexByte(s, '\n')
		if i < 0 {
			break
		}
		s = s[i+1:]
	}
	return n
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}
