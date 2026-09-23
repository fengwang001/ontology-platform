// Package lines splits byte strings into lines preserving original line
// terminators (\n, \r\n, or a missing terminator on the last line).
package lines

// Line is one split line. Data always includes the original terminator
// when one was present: "a\n", "a\r\n", or "a" for a final unterminated line.
type Line struct {
	Data    string // full line text including terminator bytes
	Content string // line text without terminator
	CRLF    bool   // terminator is \r\n
	EndNL   bool   // line has any terminator
}

// Split divides s into lines. An empty input yields zero lines.
func Split(s string) []Line {
	ls := []Line{}
	for len(s) > 0 {
		i := indexByte(s, '\n')
		if i < 0 {
			ls = append(ls, Line{Data: s, Content: s})
			break
		}
		data := s[:i+1]
		content := s[:i]
		crlf := false
		if i > 0 && content[i-1] == '\r' {
			content = content[:i-1]
			crlf = true
		}
		ls = append(ls, Line{Data: data, Content: content, CRLF: crlf, EndNL: true})
		s = s[i+1:]
	}
	return ls
}

// Join rebuilds the exact byte string the lines were split from.
func Join(ls []Line) string {
	b := make([]byte, 0, totalLen(ls))
	for i := range ls {
		b = append(b, ls[i].Data...)
	}
	return string(b)
}

// Contents returns the terminator-stripped text of each line.
func Contents(ls []Line) []string {
	out := make([]string, len(ls))
	for i := range ls {
		out[i] = ls[i].Content
	}
	return out
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func totalLen(ls []Line) int {
	n := 0
	for i := range ls {
		n += len(ls[i].Data)
	}
	return n
}
