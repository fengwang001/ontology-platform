// Package lines 把字节串切成行并保留每行原本的行尾，可原样拼回。
package lines

import "bytes"

// Split 把 b 切成行。每行保留其行尾（\n、\r\n），
// 最后一行可能没有行尾。空输入返回 nil。
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

// Join 把行重新拼成字节串，是 Split 的逆操作。
func Join(ls [][]byte) []byte {
	var n int
	for _, l := range ls {
		n += len(l)
	}
	out := make([]byte, 0, n)
	for _, l := range ls {
		out = append(out, l...)
	}
	return out
}

// Terminated 报告该行是否以 \n 结尾。
func Terminated(l []byte) bool {
	return len(l) > 0 && l[len(l)-1] == '\n'
}
