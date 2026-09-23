// Package lines 把字节串切成保留原始行尾的行序列，并可无损拼回。
package lines

// Line 是一行：Content 为不含行尾的内容，Term 为原始行尾（"\n"、"\r\n" 或 ""）。
type Line struct {
	Content string
	Term    string
}

// Split 按保留行尾的方式切分 b（仅识别 "\n"，其前的 "\r" 归入行尾）。
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
		end := i // 内容终点（不含）
		term := "\n"
		if end > start && b[end-1] == '\r' {
			end--
			term = "\r\n"
		}
		out = append(out, Line{Content: string(b[start:end]), Term: term})
		start = i + 1
	}
	if start < len(b) {
		out = append(out, Line{Content: string(b[start:]), Term: ""})
	}
	return out
}

// Join 是 Split 的逆操作，逐字节还原原始字节串。
func Join(ls []Line) []byte {
	var buf []byte
	for _, l := range ls {
		buf = append(buf, l.Content...)
		buf = append(buf, l.Term...)
	}
	return buf
}

// Bytes 返回整行（内容加行尾）。
func (l Line) Bytes() []byte { return append(append([]byte(l.Content), l.Term...)) }

// NoNL 报告该行是否没有行尾（文件最后一行）。
func (l Line) NoNL() bool { return l.Term == "" }
