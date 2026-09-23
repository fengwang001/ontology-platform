// Package lines 把字节串切成保留原始行尾的行序列，并可原样拼回。
package lines

// Line 是一行：Text 不含行尾，NL 为原本的行尾字节序列
// （"\n"、"\r\n"，或最后一行无行尾时为 nil）。
type Line struct {
	Text []byte
	NL   []byte
}

// Raw 返回该行含行尾的原始字节。
func (l Line) Raw() []byte {
	out := make([]byte, 0, len(l.Text)+len(l.NL))
	out = append(out, l.Text...)
	out = append(out, l.NL...)
	return out
}

// Split 切分字节串，保留每行原本的行尾（\n、\r\n、或末行无尾）。
// 空输入返回长度为 0 的切片（不产生空行）。
func Split(data []byte) []Line {
	if len(data) == 0 {
		return nil
	}
	var out []Line
	for len(data) > 0 {
		i := indexLF(data)
		if i < 0 {
			out = append(out, Line{Text: append([]byte(nil), data...)})
			break
		}
		text := data[:i]
		nl := data[i : i+1]
		if i > 0 && text[i-1] == '\r' {
			text = text[:i-1]
			nl = data[i-1 : i+1]
		}
		out = append(out, Line{Text: append([]byte(nil), text...), NL: append([]byte(nil), nl...)})
		data = data[i+1:]
	}
	return out
}

func indexLF(b []byte) int {
	for i, c := range b {
		if c == '\n' {
			return i
		}
	}
	return -1
}

// Join 把行序列原样拼回字节串，是 Split 的逆操作。
func Join(ls []Line) []byte {
	n := 0
	for _, l := range ls {
		n += len(l.Text) + len(l.NL)
	}
	out := make([]byte, 0, n)
	for _, l := range ls {
		out = append(out, l.Text...)
		out = append(out, l.NL...)
	}
	return out
}

// Equal 比较两行（含行尾）是否逐字节相同。
func Equal(a, b Line) bool {
	return string(a.Text) == string(b.Text) && string(a.NL) == string(b.NL)
}

// Clone 深拷贝一行。
func Clone(l Line) Line {
	return Line{Text: append([]byte(nil), l.Text...), NL: append([]byte(nil), l.NL...)}
}
