// Package lines 把字节串切成行并保留每行原始行尾，可原样拼回。
package lines

import "strings"

// Split 把 b 切成若干行，每行保留原本的行尾（\n 或 \r\n 中的 \n 与 \r）；
// 最后一行可能没有行尾。空输入返回空切片。Join(Split(b)) == b 恒成立。
func Split(b []byte) []string {
	s := string(b)
	var out []string
	for len(s) > 0 {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			out = append(out, s)
			break
		}
		out = append(out, s[:i+1])
		s = s[i+1:]
	}
	return out
}

// Join 把行切片原样拼回字节串。
func Join(ls []string) []byte {
	var sb strings.Builder
	for _, l := range ls {
		sb.WriteString(l)
	}
	return []byte(sb.String())
}
