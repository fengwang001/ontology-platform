package lines

type Line []byte

// Split 按 \n 切分，\r 归入行内容，因此 \r\n 行尾被原样保留。
func Split(b []byte) []Line {
	if len(b) == 0 {
		return nil
	}
	var out []Line
	start := 0
	for i := 0; i < len(b); i++ {
		if b[i] == '\n' {
			out = append(out, append(Line(nil), b[start:i+1]...))
			start = i + 1
		}
	}
	if start < len(b) {
		out = append(out, append(Line(nil), b[start:]...))
	}
	return out
}

// Join 把各行原样拼回；Split 后 Join 恒等于原字节。
func Join(ls []Line) []byte {
	var n int
	for _, l := range ls {
		n += len(l)
	}
	out := make([]byte, 0, n)
	for _, l := range ls {
		out = append(out, l...)
	}
	return out
}

// Equal 比较两行内容（含行尾）。
func Equal(a, b Line) bool { return string(a) == string(b) }
