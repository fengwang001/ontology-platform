// Package lines splits byte strings into logical lines while preserving the
// exact original line terminator ("\n", "\r\n", or absent for the final line).
package lines

// Line is one logical line. Content excludes the terminator; NL is the
// original terminator bytes ("\n", "\r\n", or "" for a final unterminated line).
type Line struct {
	Content string
	NL      string
}

// Split cuts s into Lines without copying semantics beyond strings.
// The empty input yields zero lines (it is not a single empty line).
func Split(s string) []Line {
	ls := []Line{}
	for len(s) > 0 {
		i := indexByte(s, '\n')
		if i < 0 {
			ls = append(ls, Line{Content: s, NL: ""})
			break
		}
		content := s[:i]
		nl := "\n"
		if len(content) > 0 && content[len(content)-1] == '\r' {
			content = content[:len(content)-1]
			nl = "\r\n"
		}
		ls = append(ls, Line{Content: content, NL: nl})
		s = s[i+1:]
	}
	return ls
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// Join rebuilds the exact original bytes from ls.
func Join(ls []Line) string {
	b := make([]byte, 0, sumLen(ls))
	for _, l := range ls {
		b = append(b, l.Content...)
		b = append(b, l.NL...)
	}
	return string(b)
}

func sumLen(ls []Line) int {
	n := 0
	for _, l := range ls {
		n += len(l.Content) + len(l.NL)
	}
	return n
}

// SplitBytes is a convenience wrapper over Split.
func SplitBytes(b []byte) []Line { return Split(string(b)) }

// EndNL reports the terminator used for normal (non-final) lines: CRLF when
// the text contains any CRLF terminator, otherwise LF.
func EndNL(ls []Line) string {
	for _, l := range ls {
		if l.NL == "\r\n" {
			return "\r\n"
		}
		if l.NL == "\n" {
			return "\n"
		}
	}
	return "\n"
}

// EndsWithNL reports whether the final physical line is terminated.
func EndsWithNL(ls []Line) bool {
	return len(ls) == 0 || ls[len(ls)-1].NL != ""
}
