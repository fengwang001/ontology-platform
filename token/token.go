// Package token 判定头部名与值的字符合法性。
//
// 名字必须是 1 个以上 tchar；值允许 HTAB、可见 ASCII 与 obs-text，
// 禁止 CR、LF、NUL、其余 C0 控制字符与 DEL。
package token

// IsTchar 报告 b 是否属于 tchar 集合（RFC 9110 §5.6.2）。
func IsTchar(b byte) bool {
	switch {
	case b >= '0' && b <= '9', b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z':
		return true
	}
	switch b {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	}
	return false
}

// IsValidName 报告 s 是否是合法的头部名（非空且全部为 tchar）。
func IsValidName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !IsTchar(s[i]) {
			return false
		}
	}
	return true
}

// IsForbiddenValueByte 报告 b 是否是值中禁止出现的字节：
// CR、LF、NUL、除 HTAB 外的 C0 控制字符、DEL。
func IsForbiddenValueByte(b byte) bool {
	switch {
	case b == '\r' || b == '\n':
		return true
	case b < 0x20 && b != '\t':
		return true
	case b == 0x7f:
		return true
	}
	return false
}

// IsValidValue 报告 s 是否是合法的头部值（无禁止字节）。
func IsValidValue(s string) bool {
	for i := 0; i < len(s); i++ {
		if IsForbiddenValueByte(s[i]) {
			return false
		}
	}
	return true
}

// HasForbiddenEscape 报告 s 是否含有可解码为禁止字节的百分号
// 编码（%00-%1F、%7F，十六进制大小写不敏感）。下游若做百分号
// 解码，这类序列会还原出 CR/LF/NUL 等禁止字节，必须一并拒绝；
// 其余百分号序列（如 %41、%25）按普通可见字符透传。
func HasForbiddenEscape(s string) bool {
	for i := 0; i+2 < len(s); i++ {
		if s[i] != '%' {
			continue
		}
		hi, ok1 := unhex(s[i+1])
		lo, ok2 := unhex(s[i+2])
		if ok1 && ok2 && IsForbiddenValueByte(hi<<4|lo) {
			return true
		}
	}
	return false
}

func unhex(c byte) (byte, bool) {
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

// Canonical 把头部名规范化为统一形态：首字母与连字符后的字母大写，
// 其余小写。名字大小写不敏感，规范化不改变语义。
func Canonical(name string) string {
	b := []byte(name)
	upper := true
	for i, c := range b {
		switch {
		case c == '-':
			upper = true
		case upper && c >= 'a' && c <= 'z':
			b[i] = c - ('a' - 'A')
			upper = false
		case !upper && c >= 'A' && c <= 'Z':
			b[i] = c + ('a' - 'A')
			upper = false
		default:
			upper = false
		}
	}
	return string(b)
}
