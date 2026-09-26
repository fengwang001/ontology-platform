// Package lines 把字节串切成“带原始行尾”的行并可原样拼回。
package lines

// Line 是一行，Term 为其原始行尾："\n"、"\r\n" 或 ""（最后一行无换行）。
type Line struct {
	Text string // 不含行尾的行内容
	Term string // 原始行尾
}

// Bytes 返回该行的原始字节。
func (l Line) Bytes() string { return l.Text + l.Term }

// NoNL 报告该行是否没有行尾（仅可能是最后一行）。
func (l Line) NoNL() bool { return l.Term == "" }

// Split 将 data 切分为行，保留 \n、\r\n 或末行无行尾三种行尾。
// 空串得到 nil；"a" 得到一行 Text:"a", Term:""。
func Split(data string) []Line {
	if data == "" {
		return nil
	}
	ls := make([]Line, 0, countLines(data))
	i := 0
	for i < len(data) {
		j := i
		for j < len(data) && data[j] != '\n' {
			j++
		}
		if j == len(data) {
			ls = append(ls, Line{Text: data[i:j]})
			break
		}
		text := data[i:j]
		term := "\n"
		if len(text) > 0 && text[len(text)-1] == '\r' {
			text = text[:len(text)-1]
			term = "\r\n"
		}
		ls = append(ls, Line{Text: text, Term: term})
		i = j + 1
	}
	return ls
}

// Join 将行原样拼回字节串。
func Join(ls []Line) string {
	if len(ls) == 0 {
		return ""
	}
	buf := make([]byte, 0, totalLen(ls))
	for _, l := range ls {
		buf = append(buf, l.Text...)
		buf = append(buf, l.Term...)
	}
	return string(buf)
}

func countLines(data string) int {
	n := 0
	for i := 0; i < len(data); i++ {
		if data[i] == '\n' {
			n++
		}
	}
	if len(data) > 0 && data[len(data)-1] != '\n' {
		n++
	}
	return n
}

func totalLen(ls []Line) int {
	n := 0
	for _, l := range ls {
		n += len(l.Text) + len(l.Term)
	}
	return n
}
