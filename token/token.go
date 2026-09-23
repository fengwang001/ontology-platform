// Package token 判定头部名与值的字符合法性。
//
// 本包不依赖工程内任何其他包。所有判定按字节（ASCII）进行：
// 头部字段在传输层就是字节，非 ASCII 字节在本设计中一律拒绝，
// 避免把多字节解码的歧义带入安全边界。
package token

import "errors"

// ErrIllegalName 表示头部名包含非 tchar 字节（包括 CR/LF/空格/冒号）。
var ErrIllegalName = errors.New("token: illegal header name byte")

// ErrIllegalValue 表示值包含禁止字节（CR/LF/NUL/控制字符/DEL/非 ASCII）。
var ErrIllegalValue = errors.New("token: illegal header value byte")

// ErrEncodedInjection 表示值的某种编码形态（%HH、\xHH、\uXXXX、\r...）
// 解码后包含禁止字节。即使传输字节本身合法，也判定为注入尝试并拒绝。
var ErrEncodedInjection = errors.New("token: encoded control character / injection")

// IsTChar 报告 b 是否为 RFC 7230 token 字符（tchar）。
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

// ValidName 要求名字非空且全部由 tchar 组成。
func ValidName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		if !IsTChar(name[i]) {
			return false
		}
	}
	return true
}

// IsValueByte 报告裸字节 b 是否允许出现在值中：
// 仅允许 HTAB(0x09) 与可见 ASCII 0x20–0x7E。
func IsValueByte(b byte) bool {
	return b == '\t' || b >= 0x20 && b <= 0x7e
}

// ValidValue 做裸字节校验：任何 CR/LF/NUL/其他控制字符/DEL/非 ASCII 都拒绝。
// 注意：本函数只看原始字节，编码形态由 ValidateValue 额外扫描。
func ValidValue(v string) bool {
	for i := 0; i < len(v); i++ {
		if !IsValueByte(v[i]) {
			return false
		}
	}
	return true
}

// ValidateValue 是设置/解析值的统一入口：
// 先做裸字节校验，再对常见编码形态解码后复核，任何一道不过即返回对应错误。
// 绝不静默删除非法字节——非法即拒绝。
func ValidateValue(v string) error {
	if !ValidValue(v) {
		return ErrIllegalValue
	}
	if decodedHasControl(v) {
		return ErrEncodedInjection
	}
	return nil
}
