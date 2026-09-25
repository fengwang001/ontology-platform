// Package lines 把字节串切成行并保留每行原始行尾，可原样拼回。
package lines

import "bytes"

// Split 把 b 切成行，每行保留其原始行尾（\n 或 \r\n）；
// 最后一行可能没有行尾。空输入返回空切片。
func Split(b []byte) [][]byte {
	var out [][]byte
	for len(b) > 0 {
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			out = append(out, b)
			break
		}
		out = append(out, b[:i+1])
		b = b[i+1:]
	}
	return out
}

// Join 把行切片原样拼回字节串，是 Split 的逆操作。
func Join(ls [][]byte) []byte {
	return bytes.Join(ls, nil)
}

// Equal 报告两行是否逐字节相同（含行尾）。
func Equal(a, b []byte) bool {
	return bytes.Equal(a, b)
}
