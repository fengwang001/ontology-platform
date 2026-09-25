package lines

// Line is one input line retaining its original terminator bytes
// ("\n", "\r\n", or empty for a final line without a newline).
type Line struct {
	Data []byte // content without terminator
	End  []byte // terminator: "\n", "\r\n", or nil
}

// Bytes returns the original line bytes: content followed by terminator.
func (l Line) Bytes() []byte {
	out := make([]byte, 0, len(l.Data)+len(l.End))
	out = append(out, l.Data...)
	out = append(out, l.End...)
	return out
}

// Equal compares full lines including terminators.
func (l Line) Equal(o Line) bool {
	return string(l.Data) == string(o.Data) && string(l.End) == string(o.End)
}

// Split partitions data into Lines, preserving every terminator.
// Empty input yields no lines; Join(Split(x)) == x byte for byte.
func Split(data []byte) []Line {
	var ls []Line
	i := 0
	for i < len(data) {
		j := i
		for j < len(data) && data[j] != '\n' {
			j++
		}
		content := data[i:j]
		end := []byte(nil)
		if j < len(data) {
			start := j
			if start > i && data[start-1] == '\r' {
				start--
			}
			content = data[i:start]
			end = data[start : j+1]
		}
		ls = append(ls, Line{Data: append([]byte(nil), content...), End: append([]byte(nil), end...)})
		i = j + 1
	}
	return ls
}

// Join reconstructs the exact original bytes.
func Join(ls []Line) []byte {
	n := 0
	for _, l := range ls {
		n += len(l.Data) + len(l.End)
	}
	out := make([]byte, 0, n)
	for _, l := range ls {
		out = append(out, l.Data...)
		out = append(out, l.End...)
	}
	return out
}

// NoNewline reports whether l is a final line without a terminator.
func NoNewline(l Line) bool { return len(l.End) == 0 }
