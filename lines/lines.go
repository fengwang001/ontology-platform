// Package lines 将字节串切分为保留原始行尾的行，并可原样拼回。
package lines

// Line 是一段以行尾结尾（或文件末尾无换行）的原始字节。
// 直接参与相等比较，因此 \n 与 \r\n、有无末尾换行均视为不同行。
type Line []byte

// Split 保留每行原本的行尾：\n、\r\n，或最后一行无行尾。
func Split(data []byte) []Line {
	if len(data) == 0 {
		return nil
	}
	var out []Line
	start := 0
	for i := 0; i < len(data); i++ {
		if data[i] != '\n' {
			continue
		}
		end := i + 1
		if end-start >= 2 && data[end-2] == '\r' {
			// \r\n 整体属于该行，无需额外处理。
		}
		out = append(out, Line(append([]byte(nil), data[start:end]...)))
		start = end
	}
	if start < len(data) {
		out = append(out, Line(append([]byte(nil), data[start:]...)))
	}
	return out
}

// Join 原样拼回，Split 后 Join 恒等于输入（含空输入）。
func Join(ls []Line) []byte {
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

// Content 返回去掉行尾（\r\n 或 \n）后的行内容。
func (l Line) Content() []byte {
	if len(l) > 0 && l[len(l)-1] == '\n' {
		if len(l) >= 2 && l[len(l)-2] == '\r' {
			return l[:len(l)-2]
		}
		return l[:len(l)-1]
	}
	return l
}

// HasNL 报告该行是否以换行结尾。
func (l Line) HasNL() bool { return len(l) > 0 && l[len(l)-1] == '\n' }
