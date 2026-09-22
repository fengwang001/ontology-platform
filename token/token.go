// Package token 判定头部名字与值的字符合法性。它不依赖其他任何包。
package token

// IsTChar 报告 b 是否为 RFC 9110 的 tchar（头部名字允许的字节）。
func IsTChar(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9':
		return true
	}
	switch b {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	}
	return false
}

// IsVChar 报告 b 是否为可见 ASCII（0x21–0x7E）。
func IsVChar(b byte) bool { return b >= 0x21 && b <= 0x7E }

// IsValueByte 报告 b 是否可作为头部值内容：可见 ASCII、空格或水平制表符。
// CR、LF、NUL 与其余控制字符一律不合法，由调用方判定为错误而非裁剪。
func IsValueByte(b byte) bool {
	return b == ' ' || b == '\t' || IsVChar(b)
}

// IsNameByte 是 IsTChar 的语义别名，供校验入口使用。
func IsNameByte(b byte) bool { return IsTChar(b) }
