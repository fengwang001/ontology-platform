// Package lines 把字节串切成保留原始行尾的行序列，并可原样拼回。
package lines

// Line 是一行：Text 为不含行尾的内容，NL 为原始行尾。
// 空切片表示这是文件最后一行且没有行尾；否则为 "\n" 或 "\r\n"。
type Line struct {
	Text string
	NL   string
}

// Content 返回内容连同原始行尾。
func (l Line) Content() string { return l.Text + l.NL }

// IsLastNoNL 报告该行是否是文件末尾且无换行。
func (l Line) IsLastNoNL() bool { return l.NL == "" }

// Split 按字节切分：\r\n 作为整体保留，单独的 \n 也是；末尾无换行的最后
// 一行 NL 为空。空输入返回长度为 0 的切片（不是一行空行）。
func Split(b []byte) []Line {
	s := string(b)
	out := make([]Line, 0, 16)
	for len(s) > 0 {
		i := indexByte(s, '\n')
		if i < 0 {
			out = append(out, Line{Text: s})
			break
		}
		text := s[:i]
		nl := "\n"
		if len(text) > 0 && text[len(text)-1] == '\r' {
			text = text[:len(text)-1]
			nl = "\r\n"
		}
		out = append(out, Line{Text: text, NL: nl})
		s = s[i+1:]
	}
	return out
}

// SplitString 是 Split 的字符串入参形式。
func SplitString(s string) []Line { return Split([]byte(s)) }

// Join 原样拼回，保证 Join(Split(b)) == b。
func Join(ls []Line) []byte {
	total := 0
	for _, l := range ls {
		total += len(l.Text) + len(l.NL)
	}
	buf := make([]byte, 0, total)
	for _, l := range ls {
		buf = append(buf, l.Text...)
		buf = append(buf, l.NL...)
	}
	return buf
}

// JoinString 是 Join 的字符串结果形式。
func JoinString(ls []Line) string { return string(Join(ls)) }

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}
