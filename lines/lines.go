// Package lines 把字节串切成保留原始行尾的行序列，并可原样拼回。
//
// 每一行包含其行尾："\n"、"\r\n"，或文件最后一行没有行尾时为空尾。
// 因此 Join(Split(b)) 对任意 b 逐字节等于 b。
package lines

import "bytes"

// Split 按 '\n' 切分并保留每个换行符（其前可能紧接 '\r'）。
func Split(b []byte) [][]byte {
	var out [][]byte
	start := 0
	for i := 0; i < len(b); i++ {
		if b[i] == '\n' {
			out = append(out, b[start:i+1])
			start = i + 1
		}
	}
	if start < len(b) {
		out = append(out, b[start:])
}
	return out
}

// Join 是 Split 的逆运算，不插入任何额外字节。
func Join(ls [][]byte) []byte { return bytes.Join(ls, nil) }

// Content 返回去掉行尾后的行内容。
func Content(line []byte) []byte {
	if bytes.HasSuffix(line, []byte("\n")) {
		line = line[:len(line)-1]
		if bytes.HasSuffix(line, []byte("\r")) {
			line = line[:len(line)-1]
		}
	}
	return line
}

// HasNL 报告该行是否以换行结尾（"\n" 或 "\r\n"）。
func HasNL(line []byte) bool { return bytes.HasSuffix(line, []byte("\n")) }

// Equal 逐字节比较两行。
func Equal(x, y []byte) bool { return bytes.Equal(x, y) }
