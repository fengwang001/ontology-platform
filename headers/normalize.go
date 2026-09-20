package headers

import "strings"

// Canonical 把键名规范化为每段首字母大写、其余小写的形式，
// 例如 "content-type" -> "Content-Type"，"x-request-id" -> "X-Request-Id"。
func Canonical(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	upper := true
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c == '-':
			upper = true
			b.WriteByte(c)
		case upper && c >= 'a' && c <= 'z':
			b.WriteByte(c - ('a' - 'A'))
			upper = false
		case !upper && c >= 'A' && c <= 'Z':
			b.WriteByte(c + ('a' - 'A'))
		default:
			b.WriteByte(c)
			upper = false
		}
	}
	return b.String()
}

// validName 校验键名：非空，不含空格、冒号与控制字符。
func validName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c <= ' ' || c == 0x7f || c == ':' {
			return false
		}
	}
	return true
}
