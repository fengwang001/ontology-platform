// Package lines 把字节串切成“内容 + 原始行尾”的行序列，且可逐字节拼回。
package lines

// Line 是一行：Data 不含行尾；End 为 "\n"、"\r\n" 或 ""（最后一行无换行）。
type Line struct {
	Data []byte
	End  []byte
}

// Raw 返回该行的原始字节（内容加行尾）。
func (l Line) Raw() []byte {
	out := make([]byte, 0, len(l.Data)+len(l.End))
	out = append(out, l.Data...)
	out = append(out, l.End...)
	return out
}

// Split 按原始行尾切分，保留 \r\n 与“最后一行无换行”信息。
// 空输入返回长度为 0 的切片；纯 "\n" 视为一行内容为空且有行尾。
func Split(b []byte) []Line {
	if len(b) == 0 {
		return nil
	}
	var ls []Line
	for len(b) > 0 {
		i := indexByte(b, '\n')
		if i < 0 {
			ls = append(ls, Line{Data: append([]byte(nil), b...), End: nil})
			break
		}
		seg := b[:i]
		end := b[i : i+1]
		data := seg
		if n := len(seg); n > 0 && seg[n-1] == '\r' {
			data = seg[:n-1]
			end = b[i-1 : i+1]
		}
		ls = append(ls, Line{
			Data: append([]byte(nil), data...),
			End:  append([]byte(nil), end...),
		})
		b = b[i+1:]
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

// Join 把行序列原样拼回，结果与 Split 的输入逐字节相等。
func Join(ls []Line) []byte {
	n := 0
	for _, l := range ls {
		n += len(l.Data) + len(l.End)
	}
	out := make([]byte, 0, n)
	for _, l := range ls {
		out = append(out, l.Data...)
		out = append(out, l.End...)
	}
	return out
}

// Equal 比较两行内容与行尾是否完全一致。
func Equal(x, y Line) bool {
	return bytesEqual(x.Data, y.Data) && bytesEqual(x.End, y.End)
}

func bytesEqual(a, b []byte) bool {
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

// Clone 深拷贝一行。
func Clone(l Line) Line {
	return Line{Data: append([]byte(nil), l.Data...), End: append([]byte(nil), l.End...)}
}
