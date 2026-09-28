// Package lines 把字节串切成保留原始行尾的行序列，并可原样拼回。
package lines

// Line 是一行：Text 不含行尾，End 为原始行尾（"\n"、"\r\n" 或 ""）。
// 最后一行无换行时 End == ""；空字节串没有任何行。
type Line struct {
	Text string
	End  string
}

// Bytes 返回该行的原始字节。
func (l Line) Bytes() []byte { return []byte(l.Text + l.End) }

// Split 保留每行行尾切分。"\r\n" 作为整体保留，孤立 '\r' 视为正文。
func Split(data []byte) []Line {
	var out []Line
	s := string(data)
	for len(s) > 0 {
		i := indexByte(s, '\n')
		if i < 0 {
			out = append(out, Line{Text: s})
			break
		}
		text := s[:i]
		end := "\n"
		if len(text) > 0 && text[len(text)-1] == '\r' {
			text = text[:len(text)-1]
			end = "\r\n"
		}
		out = append(out, Line{Text: text, End: end})
		s = s[i+1:]
	}
	return out
}

// Join 把行序列原样拼回字节串。
func Join(ls []Line) []byte {
	size := 0
	for _, l := range ls {
		size += len(l.Text) + len(l.End)
	}
	buf := make([]byte, 0, size)
	for _, l := range ls {
		buf = append(buf, l.Text...)
		buf = append(buf, l.End...)
	}
	return buf
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}
