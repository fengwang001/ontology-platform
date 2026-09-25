// Package lines 把字节串切成行，保留每行原本的行尾（\n、\r\n 或无行尾），
// 并能原样拼回。不依赖其他包。
package lines

import (
	"bytes"
	"strings"
)

// Split 把 b 切成若干行，每行保留自己的行尾（含 \r\n 中的 \r）。
// 最后一行可能没有行尾。空输入返回 nil。
func Split(b []byte) []string {
	var out []string
	for len(b) > 0 {
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			out = append(out, string(b))
			break
		}
		out = append(out, string(b[:i+1]))
		b = b[i+1:]
	}
	return out
}

// Join 把行原样拼回字节串；Join(Split(b)) 逐字节等于 b。
func Join(ls []string) []byte {
	return []byte(strings.Join(ls, ""))
}
