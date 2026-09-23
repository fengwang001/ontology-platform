// Package lines 把字节串切成“保留原始行尾”的行序列，并可原样拼回。
package lines

// Line 是一行：Content 不含行尾，NL 为原始行尾（"\n"、"\r\n" 或 ""）。
type Line struct {
	Content []byte
	NL      []byte
}

// Split 按行切分 data，保留每行原本的行尾。
// 空输入得到 0 行；"a\n" 为 1 行（NL=="\n"）；"a" 为 1 行（NL=="")。
func Split(data []byte) []Line {
	out := make([]Line, 0, countLines(data))
	i := 0
	for i < len(data) {
		j := i
		nl := []byte{}
		for j < len(data) && data[j] != '\n' {
			j++
		}
		end := j
		if j < len(data) {
			nl = []byte("\n")
			if end > i && data[end-1] == '\r' {
				end--
				nl = []byte("\r\n")
			}
			j++
		}
		content := make([]byte, end-i)
		copy(content, data[i:end])
		out = append(out, Line{Content: content, NL: nl})
		i = j
	}
	return out
}

// Join 是 Split 的逆运算，对任意 data 有 Join(Split(data)) == data。
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

// Equal 比较两行的完整字节（内容与行尾都必须一致）。
func Equal(x, y Line) bool {
	return string(x.Content) == string(y.Content) && string(x.NL) == string(y.NL)
}

func countLines(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	n := 1
	for _, c := range data {
		if c == '\n' {
			n++
		}
	}
	if data[len(data)-1] == '\n' {
		n--
	}
	return n
}
