// Package filename 从参数与处置类型定出可安全落盘的文件名。
package filename

import (
	"strings"

	"ontology/paramjoin"
)

// Fallback 是被剔除到空（或整体危险）时的确定兜底名。
const Fallback = "unnamed"

// Decide 定出最终文件名：filename* 优先于 filename，同类取首个出现者。
func Decide(_ string, params []paramjoin.Param) string {
	var plain, ext string
	var hasPlain, hasExt bool
	for _, p := range params {
		if p.Name != "filename" {
			continue
		}
		if p.Ext && !hasExt {
			ext, hasExt = p.Value, true
		}
		if !p.Ext && !hasPlain {
			plain, hasPlain = p.Value, true
		}
	}
	if hasExt {
		return Sanitize(ext)
	}
	return Sanitize(plain)
}

// Sanitize 把任意字符串变成可安全落盘的文件名，次序见 NOTES.md。
func Sanitize(s string) string {
	s = strings.Trim(pctDecode(s), " \t\r\n")
	if s == "" || s == "." || s == ".." {
		return Fallback
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		dotRun := c == '.' && (i > 0 && s[i-1] == '.' || i+1 < len(s) && s[i+1] == '.')
		if c == '/' || c == '\\' || c < 0x20 || c == 0x7f || dotRun {
			const hex = "0123456789ABCDEF"
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		} else {
			b.WriteByte(c)
		}
	}
	if b.Len() == 0 {
		return Fallback
	}
	return b.String()
}

// pctDecode 防御性还原合法的 %XX，非法序列原样保留。
func pctDecode(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]) {
			b.WriteByte(hexVal(s[i+1])<<4 | hexVal(s[i+2]))
			i += 2
		} else {
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

func isHex(c byte) bool { return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F' }

func hexVal(c byte) byte {
	if c <= '9' {
		return c - '0'
	}
	if c <= 'F' {
		return c - 'A' + 10
	}
	return c - 'a' + 10
}
