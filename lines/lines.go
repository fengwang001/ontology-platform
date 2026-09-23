// Package lines 把字节串切成保留原始行尾的逻辑行，并可原样拼回。
package lines

// Line 是一个逻辑行。Text 不含任何行尾字符；CRLF 标记行尾是否为 \r\n；
// NL 标记该行是否以换行结尾（最后一行可能为 false）。
type Line struct {
	Text string
	CRLF bool
	NL   bool
}

// Split 按 \n 切分 data 并保留每行的原始行尾信息。
// 空字节串产生 0 行；末尾单个 \n 不会产生多余的空行。
func Split(data []byte) []Line {
	ls := make([]Line, 0, countLines(data))
	for len(data) > 0 {
		i := 0
		for i < len(data) && data[i] != '\n' {
			i++
		}
		if i == len(data) {
			ls = append(ls, Line{Text: string(data), NL: false})
			break
		}
		text := data[:i]
		crlf := false
		if len(text) > 0 && text[len(text)-1] == '\r' {
			text = text[:len(text)-1]
			crlf = true
		}
		ls = append(ls, Line{Text: string(text), CRLF: crlf, NL: true})
		data = data[i+1:]
	}
	return ls
}

// Join 是 Split 的逆操作：逐行按原行尾做字节拼接。
func Join(ls []Line) []byte {
	n := 0
	for _, l := range ls {
		n += len(l.Text)
		if l.NL {
			n++
			if l.CRLF {
				n++
			}
		}
	}
	buf := make([]byte, 0, n)
	for _, l := range ls {
		buf = append(buf, l.Text...)
		if l.NL {
			if l.CRLF {
				buf = append(buf, '\r')
			}
			buf = append(buf, '\n')
		}
	}
	return buf
}

// Equal 报告两个行序列是否逐行相同（含行尾信息）。
func Equal(a, b []Line) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func countLines(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	n := 1
	for _, c := range data {
		if c == '\n' {
			n++
		}
	}
	if data[len(data)-1] == '\n' {
		n--
	}
	return n
}
