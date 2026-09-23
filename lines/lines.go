// Package lines splits byte strings into newline-terminated (or final
// unterminated) lines preserving original endings, and joins them back.
package lines

import "bytes"

// Line is one logical line. Text excludes the terminator; NL is the
// original terminator ("\n" or "\r\n") or nil when the final line has none.
type Line struct {
	Text []byte
	NL   []byte
}

// Split cuts s into lines preserving every original line ending.
func Split(s []byte) []Line {
	if len(s) == 0 {
		return nil
	}
	var out []Line
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] != '\n' {
			continue
		}
		end := i // index of '\n'
		textEnd := end
		nlLen := 1
		if end > start && s[end-1] == '\r' {
			textEnd = end - 1
			nlLen = 2
		}
		out = append(out, Line{Text: s[start:textEnd], NL: s[end+1-nlLen : end+1]})
		start = i + 1
	}
	if start < len(s) {
		out = append(out, Line{Text: s[start:], NL: nil})
	}
	return out
}

// Join concatenates lines back to the exact original bytes.
func Join(ls []Line) []byte {
	var out []byte
	for _, l := range ls {
		out = append(out, l.Text...)
		out = append(out, l.NL...)
	}
	return out
}

// Equal reports content-and-ending equality.
func Equal(x, y Line) bool {
	return bytes.Equal(x.Text, y.Text) && bytes.Equal(x.NL, y.NL)
}
