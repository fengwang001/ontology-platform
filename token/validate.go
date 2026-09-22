package token

import "errors"

// 确定性错误：校验失败的三种类别，彼此可判定区分。
var (
	ErrInvalidName  = errors.New("token: header name contains an illegal byte")
	ErrInvalidValue = errors.New("token: header value contains a forbidden control byte")
	ErrEncodedBreak = errors.New("token: header value contains an encoded line-break sequence")
)

// ValidateName 校验头部名字：非空且每个字节都是 tchar。
func ValidateName(name string) error {
	if name == "" {
		return ErrInvalidName
	}
	for i := 0; i < len(name); i++ {
		if !IsNameByte(name[i]) {
			return ErrInvalidName
		}
	}
	return nil
}

// ValidateValue 校验头部值。
//
// 硬拒绝两类内容：
//  1. 原始控制字节（NUL/CR/LF/其他 C0/DEL；HTAB 允许，供折行解析兼容）；
//  2. 编码形态的换行序列（%0d %0a、\r \n、\u000d 等，大小写与引号无关）。
//     头部值是不透明 token，本库不提供任何编码/解码约定；这些序列可能被
//     下游二次解码成真实换行，属多实现分歧的走私面，故在入口单点拒绝。
// 绝不静默删除任何字节。
func ValidateValue(value string) error {
	for i := 0; i < len(value); i++ {
		b := value[i]
		if b == '\t' || b == ' ' || IsVChar(b) {
			continue
		}
		return ErrInvalidValue
	}
	if hasEncodedBreak(value) {
		return ErrEncodedBreak
	}
	return nil
}

// hasEncodedBreak 在不分配的前提下扫描常见的“编码换行”字面序列。
func hasEncodedBreak(v string) bool {
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case c == '%' && i+2 < len(v) && isHex(v[i+1]) && isHex(v[i+2]):
			h := hexVal(v[i+1]); l := hexVal(v[i+2]); x := h<<4 | l
			if x == '\r' || x == '\n' || x == 0 {
				return true
			}
		case c == '\\' && i+1 < len(v):
			n := lower(v[i+1])
			if n == 'r' || n == 'n' {
				return true
			}
			if n == '0' && i+3 <= len(v) && isHex(v[i+2]) && isHex(v[i+3]) {
				if hexVal(v[i+2]) == 0 && hexVal(v[i+3]) == 0 {
					return true
				}
			}
			if n == 'x' || n == 'u' {
				if hexLikeCRLF(v[i+2:]) {
					return true
				}
			}
		}
	}
	return false
}

func hexLikeCRLF(s string) bool {
	const max = 6
	n := len(s)
	if n > max {
		n = max
	}
	var digits []byte
	for i := 0; i < n; i++ {
		if isHex(s[i]) {
			digits = append(digits, s[i])
			continue
		}
		break
	}
	if len(digits) < 2 {
		return false
	}
	for i := 0; i+1 < len(digits); i += 2 {
		if hexVal(digits[i])<<4|hexVal(digits[i+1]) == '\r' ||
			hexVal(digits[i])<<4|hexVal(digits[i+1]) == '\n' {
			return true
		}
	}
	return false
}

func isHex(b byte) bool {
	return (b >= '0' && b <= '9') || lower(b) >= 'a' && lower(b) <= 'f'
}

func hexVal(b byte) byte {
	switch {
	case b >= '0' && b <= '9':
		return b - '0'
	default:
		return lower(b) - 'a' + 10
	}
}

func lower(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + ('a' - 'A')
	}
	return b
}
