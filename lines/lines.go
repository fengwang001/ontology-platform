// Package lines splits byte strings into lines that keep their original
// line endings, and joins them back losslessly.
package lines

import "strings"

// Line is a single line. Text excludes the line ending; End is the
// original ending ("\n" or "\r\n"), empty for a final unterminated line.
type Line struct {
	Text string
	End  string
}

// Raw returns the line exactly as it appeared in the input.
func (l Line) Raw() string { return l.Text + l.End }

// Split divides b into lines. Each line keeps its original ending; the
// last line may have no ending. Split(nil) returns nil.
func Split(b []byte) []Line {
	s := string(b)
	var out []Line
	for len(s) > 0 {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			out = append(out, Line{Text: s})
			break
		}
		text, end := s[:i], "\n"
		if i > 0 && s[i-1] == '\r' {
			text, end = s[:i-1], "\r\n"
		}
		out = append(out, Line{Text: text, End: end})
		s = s[i+1:]
	}
	return out
}

// Join is the exact inverse of Split.
func Join(ls []Line) []byte {
	n := 0
	for _, l := range ls {
		n += len(l.Text) + len(l.End)
	}
	b := make([]byte, 0, n)
	for _, l := range ls {
		b = append(b, l.Text...)
		b = append(b, l.End...)
	}
	return b
}
