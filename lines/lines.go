// Package lines 把字节串切成行并保留原始行尾。
package lines

// Line 是一行：Content 不含行尾，EOL 为 "\n"、"\r\n" 或 ""（最后一行无行尾）。
type Line struct {
	Content string
	EOL     string
}

// Raw 返回该行的原始字节形式（含行尾）。
func (l Line) Raw() string { return l.Content + l.EOL }

// Split 把 data 切成 Line 切片，保留每行原本的 \n、\r\n 或“无行尾”。
// 空输入返回 nil；Split 的逆操作是 Join。
func Split(data string) []Line {
	if len(data) == 0 {
		return nil
	}
	var out []Line
	start := 0
	for i := 0; i < len(data); {
		if data[i] == '\n' {
			eol := 1
			if i > start && data[i-1] == '\r' {
				eol = 2
			}
			out = append(out, Line{Content: data[start : i-eol+1], EOL: data[i-eol+1 : i+1]})
			i++
			start = i
			continue
		}
		i++
	}
	if start < len(data) {
		out = append(out, Line{Content: data[start:], EOL: ""})
	}
	return out
}

// Join 把行序列原样拼回字节串，是 Split 的逆。
func Join(ls []Line) string {
	if len(ls) == 0 {
		return ""
	}
	buf := make([]byte, 0, len(ls)*16)
	for _, l := range ls {
		buf = append(buf, l.Content...)
		buf = append(buf, l.EOL...)
	}
	return string(buf)
}
