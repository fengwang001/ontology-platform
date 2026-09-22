package rangespec

// SyntaxError 表示 Range 头在词法/语法层面不合法。
// Offset 是出错的字节偏移（从 Range 头 "bytes=" 之后的第一个字节起算）。
type SyntaxError struct {
	Offset int
	Reason string
}

func (e *SyntaxError) Error() string {
	return "rangespec: syntax error at offset " + itoa(e.Offset) + ": " + e.Reason
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
