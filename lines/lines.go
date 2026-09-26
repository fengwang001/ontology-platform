package lines

// Line is one logical line with its original terminator preserved.
type Line struct {
	Content []byte
	Newline []byte
}

// Split divides data into lines. An empty file has no lines.
func Split(data []byte) []Line {
	var out []Line
	for len(data) > 0 {
		i := indexByte(data, '\n')
		if i < 0 {
			out = append(out, Line{Content: append([]byte(nil), data...)})
			break
		}
		content := data[:i]
		nl := []byte{'\n'}
		if i > 0 && content[i-1] == '\r' {
			content = content[:i-1]
			nl = []byte{'\r', '\n'}
		}
		out = append(out, Line{
			Content: append([]byte(nil), content...),
			Newline: nl,
		})
		data = data[i+1:]
	}
	return out
}

// Join reconstructs the exact bytes represented by lines.
func Join(lines []Line) []byte {
	var out []byte
	for _, line := range lines {
		out = append(out, line.Content...)
		out = append(out, line.Newline...)
	}
	return out
}

func indexByte(data []byte, b byte) int {
	for i, c := range data {
		if c == b {
			return i
		}
	}
	return -1
}
