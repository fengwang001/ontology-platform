// Package lines 在“保留原始行尾”的前提下切分与拼回字节串。
//
// 每个切片元素都是一整行：以 "\n" 结尾（此时前缀可能是 "\r"，即 CRLF），
// 或为输入末尾没有换行的最后一行。Split 与 Join 互逆：
// bytes.Join 不会做任何行尾规整，CRLF 与“末尾无换行”均原样保留。
package lines

// Split 把 data 切成行，保留每行原本的行尾。
// 空输入产生 nil（0 行）；"a" 产生一行无尾换行的 "a"。
func Split(data []byte) [][]byte {
	if len(data) == 0 {
		return nil
	}
	var out [][]byte
	start := 0
	for i := 0; i < len(data); i++ {
		if data[i] == '\n' {
			out = append(out, data[start:i+1:i+1])
			start = i + 1
		}
	}
	if start < len(data) {
		out = append(out, data[start:len(data):len(data)])
	}
	return out
}

// Join 原样拼回行序列，是 Split 的逆操作。
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

// HasNL 报告该行是否带换行结尾（LF 或 CRLF 都算）。
func HasNL(l []byte) bool { return len(l) > 0 && l[len(l)-1] == '\n' }

// Content 返回去掉行尾（"\n" 及其前导 "\r"）后的行内容。
func Content(l []byte) []byte {
	if !HasNL(l) {
		return l
	}
	l = l[:len(l)-1]
	if len(l) > 0 && l[len(l)-1] == '\r' {
		l = l[:len(l)-1]
	}
	return l
}

// Ending 返回该行的行尾："\r\n"、"\n" 或 ""（最后一行无换行）。
func Ending(l []byte) []byte {
	switch {
	case len(l) >= 2 && l[len(l)-2] == '\r':
		return l[len(l)-2:]
	case HasNL(l):
		return l[len(l)-1:]
	default:
		return nil
	}
}

// Equal 报告两行（含行尾）是否逐字节相等。
func Equal(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Clone 返回一行的独立拷贝，避免与输入底层数组别名。
func Clone(l []byte) []byte {
	c := make([]byte, len(l))
	copy(c, l)
	return c
}
