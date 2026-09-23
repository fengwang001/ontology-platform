// Package lines splits byte strings into lines while preserving original
// line terminators (\n, \r\n, or a missing terminator on the final line).
package lines

// Line is one logical line. Bytes includes the original terminator, if any.
// Content is the line without its terminator. NL records the terminator.
type Line struct {
	Bytes   []byte
	Content []byte
	NL      []byte // "\n", "\r\n", or nil for the final unterminated line
}

// Split cuts data into Lines. An empty input yields zero lines.
func Split(data []byte) []Line {
	if len(data) == 0 {
		return nil
	}
	out := make([]Line, 0, countLines(data))
	start := 0
	for i := 0; i < len(data); i++ {
		if data[i] != '\n' {
			continue
		}
		end := i + 1
		contentEnd := i
		nl := []byte{'\n'}
		if i > start && data[i-1] == '\r' {
			contentEnd = i - 1
			nl = []byte{'\r', '\n'}
		}
		out = append(out, Line{
			Bytes:   data[start:end],
			Content: data[start:contentEnd],
			NL:      nl,
		})
		start = end
	}
	if start < len(data) {
		out = append(out, Line{
			Bytes:   data[start:],
			Content: data[start:],
			NL:      nil,
		})
	}
	return out
}

// Join reconstructs the original bytes exactly.
func Join(ls []Line) []byte {
	var n int
	for _, l := range ls {
		n += len(l.Bytes)
	}
	out := make([]byte, 0, n)
	for _, l := range ls {
		out = append(out, l.Bytes...)
	}
	return out
}

// Equal reports whether two lines have identical content (terminators ignored).
func Equal(a, b Line) bool {
	return string(a.Content) == string(b.Content)
}

func countLines(data []byte) int {
	n := 0
	for _, b := range data {
		if b == '\n' {
			n++
		}
	}
	if len(data) > 0 && data[len(data)-1] != '\n' {
		n++
	}
	return n
}
