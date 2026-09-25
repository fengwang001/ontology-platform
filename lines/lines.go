package lines

// Line 表示一行：Data 不含行尾，NL 为原始行尾（"\n"、"\r\n" 或 nil）。
type Line struct {
	Data []byte
	NL   []byte
}

// Split 把字节串切成行并保留原始行尾。
func Split(b []byte) []Line {
	var out []Line
	for len(b) > 0 {
		i := indexByte(b, '\n')
		if i < 0 {
			out = append(out, Line{Data: append([]byte(nil), b...), NL: nil})
			break
		}
		raw := b[:i]
		nl := []byte{'\n'}
		if i > 0 && raw[i-1] == '\r' {
			raw = raw[:i-1]
			nl = []byte{'\r', '\n'}
		}
		out = append(out, Line{Data: append([]byte(nil), raw...), NL: nl})
		b = b[i+1:]
	}
	return out
}

// Join 原样拼回。
func Join(ls []Line) []byte {
	n := 0
	for _, l := range ls {
		n += len(l.Data) + len(l.NL)
	}
	out := make([]byte, 0, n)
	for _, l := range ls {
		out = append(out, l.Data...)
		out = append(out, l.NL...)
	}
	return out
}

// Terminated 报告该行是否带行尾。
func (l Line) Terminated() bool {
	return len(l.NL) > 0
}

// Equal 比较两行（数据与行尾都相同）。
func Equal(a, b Line) bool {
	if string(a.NL) != string(b.NL) {
		return false
	}
	return string(a.Data) == string(b.Data)
}

func indexByte(b []byte, c byte) int {
	for i := range b {
		if b[i] == c {
			return i
		}
	}
	return -1
}
