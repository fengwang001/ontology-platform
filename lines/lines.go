// Package lines 把字节串切成行并保留每行原始行尾，可原样拼回。
package lines

// Line 是一行原文，包含其行尾（"\n"、"\r\n" 或最后一行无行尾）。
type Line []byte

// Split 把 b 切成行，每行保留原始行尾。空输入返回 nil。
func Split(b []byte) []Line {
	var out []Line
	for len(b) > 0 {
		i := 0
		for i < len(b) && b[i] != '\n' {
			i++
		}
		if i < len(b) {
			i++
		}
		out = append(out, b[:i])
		b = b[i:]
	}
	return out
}

// Join 把行序列原样拼回字节串，是 Split 的逆运算。
func Join(ls []Line) []byte {
	n := 0
	for _, l := range ls {
		n += len(l)
	}
	out := make([]byte, 0, n)
	for _, l := range ls {
		out = append(out, l...)
	}
	return out
}

// Text 返回去掉行尾 "\n" 后的内容（"\r\n" 的 "\r" 保留在内容里），
// 以及该行原本是否带 "\n" 行尾。
func Text(l Line) (body string, eol bool) {
	if n := len(l); n > 0 && l[n-1] == '\n' {
		return string(l[:n-1]), true
	}
	return string(l), false
}

// Make 由内容与是否有行尾重建一行，是 Text 的逆运算。
func Make(body string, eol bool) Line {
	if eol {
		return Line(append([]byte(body), '\n'))
	}
	return Line(body)
}
