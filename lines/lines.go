// Package lines 把字节串切成保留原始行尾的行，并可原样拼回。
package lines

import "bytes"

// Line 是一行：Content 不含行尾，NL 为原始行尾（"\n"、"\r\n" 或 ""）。
type Line struct {
	Content []byte
	NL      string
}

// Split 切分字节串，保留每行行尾。
func Split(data []byte) []Line {
	var ls []Line
	for len(data) > 0 {
		i := indexByte(data, '\n')
		if i < 0 {
			ls = append(ls, Line{Content: append([]byte(nil), data...)})
			break
		}
		content, nl := data[:i], "\n"
		if i > 0 && content[i-1] == '\r' {
			content, nl = content[:i-1], "\r\n"
		}
		ls = append(ls, Line{Content: append([]byte(nil), content...), NL: nl})
		data = data[i+1:]
	}
	return ls
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}

// Join 把行原样拼回字节串。
func Join(ls []Line) []byte {
	n := 0
	for _, l := range ls {
		n += len(l.Content) + len(l.NL)
	}
	out := make([]byte, 0, n)
	for _, l := range ls {
		out = append(out, l.Content...)
		out = append(out, l.NL...)
	}
	return out
}

// Equal 比较两行（含内容，不含行尾）。
// Equal 比较两行的内容与行尾：仅末尾换行有无不同也视为不同行。
func Equal(x, y Line) bool {
	return bytes.Equal(x.Content, y.Content) && x.NL == y.NL
}
