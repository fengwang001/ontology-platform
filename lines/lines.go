// Package lines 把字节串切成行并保留每行原始行尾，可无损拼回。
package lines

// Split 把 b 切成行，每行保留其原始行尾（\n 或 \r\n）；
// 最后一行可能没有行尾。空输入返回 nil。
func Split(b []byte) [][]byte {
	var out [][]byte
	for len(b) > 0 {
		i := 0
		for i < len(b) && b[i] != '\n' {
			i++
		}
		if i < len(b) {
			i++ // 包含 '\n'
		}
		out = append(out, b[:i])
		b = b[i:]
	}
	return out
}

// Join 把行序列原样拼回字节串，是 Split 的逆运算。
func Join(ls [][]byte) []byte {
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
