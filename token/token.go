// Package token 判定头部名与值的字符合法性。它不依赖本工程任何其他包。
package token

import (
	"errors"
	"strings"
	"unicode"
)

// 头部值相关错误，全部可判定（errors.Is）。
var (
	ErrEmptyName   = errors.New("token: empty header name")
	ErrBadNameChar = errors.New("token: illegal byte in header name")
	ErrEmptyValue  = errors.New("token: empty header value")
	ErrBadValue    = errors.New("token: forbidden byte or encoding in header value")
)

// IsTchar 报告 b 是否为 RFC 7230 的 tchar（头部名允许字符）。
func IsTchar(b byte) bool {
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

// IsOWS 报告 b 是否为可选空白（SP / HTAB）。
func IsOWS(b byte) bool { return b == ' ' || b == '\t' }

// IsVchar 报告 b 是否为可见 ASCII（0x21–0x7E）。
func IsVchar(b byte) bool { return b >= 0x21 && b <= 0x7E }

// IsObsText 报告 b 是否为 obs-text（0x80–0xFF，RFC 7230 字段值允许）。
func IsObsText(b byte) bool { return b >= 0x80 }

// IsValueByte 报告 b 是否可作为已展开字段值的一个字节：
// VCHAR / SP / HTAB / obs-text。CR、LF、NUL 等控制字符一律非法。
func IsValueByte(b byte) bool {
	return IsVchar(b) || IsOWS(b) || IsObsText(b)
}

// ValidateName 校验头部名：非空且全部为 tchar。
func ValidateName(name string) error {
	if name == "" {
		return ErrEmptyName
	}
	for i := 0; i < len(name); i++ {
		if !IsTchar(name[i]) {
			return ErrBadNameChar
		}
	}
	return nil
}

// ValidateValue 校验头部值。这是注入防御唯一入口，解析与 Set/Add 共用。
//
// 拒绝两类内容：
//  1. 原始禁止字节：CR/LF、NUL 及其他 C0 控制字符、DEL（静默删除会改变
//     语义却返回成功，故必须拒绝而非清洗）；
//  2. 解码后为 CR/LF/NUL 的常见编码绕过：百分号编码（%0d/%0a/%00，
//     大小写不敏感）与反斜杠转义（\r \n \0）。解析器虽不解码，但下游
//     若多解码一层就会产生头部走私，在此一并拒绝。
func ValidateValue(v string) error {
	if v == "" {
		return ErrEmptyValue
	}
	for i := 0; i < len(v); i++ {
		b := v[i]
		if !IsValueByte(b) {
			return ErrBadValue
		}
		if b == '%' && i+2 < len(v) && isHex(v[i+1]) && isHex(v[i+2]) {
			h := hexVal(v[i+1])<<4 | hexVal(v[i+2])
			if h == 0x0A || h == 0x0D || h == 0x00 {
				return ErrBadValue
			}
		}
		if b == '\\' && i+1 < len(v) {
			switch v[i+1] {
			case 'r', 'n', '0':
				return ErrBadValue
			}
		}
	}
	return nil
}

func isHex(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'f' || b >= 'A' && b <= 'F'
}

func hexVal(b byte) int {
	switch {
	case b >= '0' && b <= '9':
		return int(b - '0')
	case b >= 'a' && b <= 'f':
		return int(b-'a') + 10
	default:
		return int(b-'A') + 10
	}
}

// LowerName 返回比较用的小写键。
func LowerName(name string) string { return strings.ToLower(name) }

// CanonicalName 把头部名规范化：首字母及每个 '-' 后字母大写，其余小写。
// 只处理 ASCII；非 ASCII 名字在 ValidateName 阶段已被拒绝。
func CanonicalName(name string) string {
	var sb strings.Builder
	sb.Grow(len(name))
	upper := true
	for i := 0; i < len(name); i++ {
		b := name[i]
		if b == '-' {
			upper = true
			sb.WriteByte(b)
			continue
		}
		if upper {
			sb.WriteRune(unicode.ToUpper(rune(b)))
		} else {
			sb.WriteRune(unicode.ToLower(rune(b)))
		}
		upper = false
	}
	return sb.String()
}
