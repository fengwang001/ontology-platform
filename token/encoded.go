package token

// decodedHasControl 对值做编码形态扫描：把百分号编码、反斜杠十六进制、
// 反斜杠 unicode 以及反斜杠字母转义（r/n/0/t/b/f/v）解码出来，
// 若任一解码结果是禁止字节则返回 true。纯文本扫描，不修改原值。
func decodedHasControl(v string) bool {
	for i := 0; i < len(v); i++ {
		switch v[i] {
		case '%':
			if b, ok := hexByte(v[i+1:]); ok && !IsValueByte(b) {
				return true
			}
		case '\\':
			if b, ok := decodeEscape(v[i+1:]); ok && !IsValueByte(b) {
				return true
			}
		}
	}
	return false
}

// hexByte 解析 s 开头的两个十六进制字符。
func hexByte(s string) (byte, bool) {
	if len(s) < 2 {
		return 0, false
	}
	hi, ok1 := hexVal(s[0])
	lo, ok2 := hexVal(s[1])
	if !ok1 || !ok2 {
		return 0, false
	}
	return hi<<4 | lo, true
}

func hexVal(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// decodeEscape 解析 s 开头的转义序列，返回其代表的字节。
// 支持单字母转义、x 加两位十六进制、u 加四位十六进制（取低字节判定）。
func decodeEscape(s string) (byte, bool) {
	if len(s) == 0 {
		return 0, false
	}
	switch s[0] {
	case 'r':
		return '\r', true
	case 'n':
		return '\n', true
	case '0':
		return 0, true
	case 't':
		return '\t', true
	case 'b':
		return '\b', true
	case 'f':
		return '\f', true
	case 'v':
		return '\v', true
	case 'x':
		return hexByte(s[1:])
	case 'u':
		if len(s) < 5 {
			return 0, false
		}
		var code uint16
		for i := 1; i <= 4; i++ {
			d, ok := hexVal(s[i])
			if !ok {
				return 0, false
			}
			code = code<<4 | uint16(d)
		}
		return byte(code), true
	}
	return 0, false
}
