// Package lines splits byte content into logical lines while preserving the
// original line terminator (LF, CRLF, or none for the final line).
package lines

import "bytes"

// Line is one logical line. Content excludes the terminator; End is the
// original terminator ("\n", "\r\n", or "" for a final line without newline).
type Line struct {
	Content []byte
	End     string
}

// NL reports whether the line was terminated by a newline.
func (l Line) NL() bool { return l.End != "" }

// Bytes returns content followed by its original terminator.
func (l Line) Bytes() []byte { return append(append([]byte{}, l.Content...), l.End...) }

// Split cuts data into Lines, retaining every original terminator.
func Split(data []byte) []Line {
	var out []Line
	for len(data) > 0 {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			out = append(out, Line{Content: append([]byte{}, data...)})
			break
		}
		content := data[:i]
		end := "\n"
		if len(content) > 0 && content[len(content)-1] == '\r' {
			content = content[:len(content)-1]
			end = "\r\n"
		}
		out = append(out, Line{Content: append([]byte{}, content...), End: end})
		data = data[i+1:]
	}
	return out
}

// Join reconstructs the exact original bytes from a line slice.
func Join(ls []Line) []byte {
	var buf bytes.Buffer
	for _, l := range ls {
		buf.Write(l.Content)
		buf.WriteString(l.End)
	}
	return buf.Bytes()
}

// Equal reports whether two lines have identical content and terminator.
func Equal(x, y Line) bool { return x.End == y.End && bytes.Equal(x.Content, y.Content) }
