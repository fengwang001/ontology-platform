package lines

// Split 按原始行尾切分，空输入返回零行。
func Split(b []byte) []Line {
	if len(b) == 0 {
		return nil
	}
	var out []Line
	start := 0
	for i := 0; i < len(b); i++ {
		if b[i] != '\n' {
			continue
		}
		nl := "\n"
		textEnd := i
		if i > start && b[i-1] == '\r' {
			nl = "\r\n"
			textEnd = i - 1
		}
		out = append(out, Line{Text: string(b[start:textEnd]), NL: nl})
		start = i + 1
	}
	if start < len(b) {
		out = append(out, Line{Text: string(b[start:]), NL: ""})
	}
	return out
}

// Join 原样拼回。
func Join(ls []Line) []byte {
	n := 0
	for _, l := range ls {
		n += len(l.Text) + len(l.NL)
	}
	buf := make([]byte, 0, n)
	for _, l := range ls {
		buf = append(buf, l.Text...)
		buf = append(buf, l.NL...)
	}
	return buf
}

// Line 表示一行：Text 不含行尾，NL 为原始行尾（"\n"、"\r\n" 或 ""）。
type Line struct {
	Text string
	NL   string
}
