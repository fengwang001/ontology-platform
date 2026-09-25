// Package lines 把字节串切成行并原样拼回。
// 每行保留原本的行尾（\n、\r\n，或最后一行无行尾），
// 因此 Join(Split(b)) 与 b 逐字节相等。
package lines

// Split 把 b 切成若干行，每个元素包含自己的行尾（若有）。
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

// Join 把若干行原样拼回字节串。
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
