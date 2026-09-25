// Package lines 把字节串切成保留原始行尾的行序列，并可无损拼回。
package lines

// Line 是一行：Data 含行尾（"\n" 或 "\r\n"），末行无行尾时 NL 为 false。
type Line struct {
	Data string // 含行尾的原始字节
	NL   bool   // 该行是否以换行结束
}

// Body 返回不含行尾的行内容。
func (l Line) Body() string {
	if l.NL {
		if len(l.Data) >= 2 && l.Data[len(l.Data)-2] == '\r' {
			return l.Data[:len(l.Data)-2]
		}
		return l.Data[:len(l.Data)-1]
	}
	return l.Data
}

// CRLF 报告行尾是否为 "\r\n"。
func (l Line) CRLF() bool {
	return l.NL && len(l.Data) >= 2 && l.Data[len(l.Data)-2] == '\r'
}

// Split 按 \n 切分，保留每个 \n 及其前导 \r；空串返回 nil。
func Split(s string) []Line {
	if len(s) == 0 {
		return nil
	}
	var out []Line
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, Line{Data: s[start : i+1], NL: true})
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, Line{Data: s[start:], NL: false})
	}
	return out
}

// Join 原样拼回行序列。
func Join(ls []Line) string {
	n := 0
	for _, l := range ls {
		n += len(l.Data)
	}
	b := make([]byte, 0, n)
	for _, l := range ls {
		b = append(b, l.Data...)
	}
	return string(b)
}

// Bodies 仅取行内容（供展示/比较辅助使用）。
func Bodies(ls []Line) []string {
	out := make([]string, len(ls))
	for i, l := range ls {
		out[i] = l.Body()
	}
	return out
}
