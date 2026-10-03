package fsmapper

import (
	"strings"
	"unicode/utf8"
)

const hexUpper = "0123456789ABCDEF"

// validSrcName 检查 src 是否非空、不含 '/' 与 NUL、为合法 UTF-8。
func validSrcName(src string) bool {
	if src == "" {
		return false
	}
	if strings.IndexByte(src, '/') >= 0 || strings.IndexByte(src, 0) >= 0 {
		return false
	}
	if !utf8.ValidString(src) {
		return false
	}
	return true
}

// mustEscapeByte 报告某字节（属于八个禁用字符或控制字符或 '%'）是否需转义。
// 调用前已确认 src 为合法 UTF-8：对多字节 rune 的后续字节（>=0x80）
// 不应按单字节规则转义。
func mustEscapeASCII(b byte) bool {
	if b == '%' {
		return true
	}
	if b < 0x20 {
		return true
	}
	switch b {
	case '<', '>', ':', '"', '\\', '|', '?', '*':
		return true
	}
	return false
}

// escapeStep 第一步：逐个 rune 转义禁用字符、控制字符与 '%'。
func escapeStep(src string) string {
	var b strings.Builder
	b.Grow(len(src))
	for i := 0; i < len(src); {
		r, size := utf8.DecodeRuneInString(src[i:])
		if size == 1 && r < utf8.RuneSelf {
			c := src[i]
			if mustEscapeASCII(c) {
				b.WriteByte('%')
				b.WriteByte(hexUpper[c>>4])
				b.WriteByte(hexUpper[c&0xF])
			} else {
				b.WriteByte(c)
			}
		} else {
			b.WriteString(src[i : i+size])
		}
		i += size
	}
	return b.String()
}

// dotSpaceStep 第二步：以 '.' 或空格结尾时替换最后一个字符。
func dotSpaceStep(s string) string {
	switch s[len(s)-1] {
	case '.':
		return s[:len(s)-1] + "%2E"
	case ' ':
		return s[:len(s)-1] + "%20"
	}
	return s
}

// reservedStep 第三步：主干（第一个 '.' 之前）ASCII 折叠后为保留名时，
// 把整个名字的第一个字符替换为 '%' 加两位大写十六进制。
func reservedStep(s string) string {
	stem := s
	if i := strings.IndexByte(s, '.'); i >= 0 {
		stem = s[:i]
	}
	if isReserved(asciiFold(stem)) {
		return "%" + string(hexUpper[s[0]>>4]) + string(hexUpper[s[0]&0xF]) + s[1:]
	}
	return s
}

func asciiFold(s string) string {
	hasUpper := false
	for i := 0; i < len(s); i++ {
		if s[i] >= 'A' && s[i] <= 'Z' {
			hasUpper = true
			break
		}
	}
	if !hasUpper {
		return s
	}
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}

func isReserved(stemFolded string) bool {
	switch stemFolded {
	case "con", "prn", "aux", "nul":
		return true
	}
	if len(stemFolded) == 4 {
		switch stemFolded[:3] {
		case "com", "lpt":
			c := stemFolded[3]
			if c >= '1' && c <= '9' {
				return true
			}
		}
	}
	return false
}

// fnv1a32Hex 返回 s 的 32 位 FNV-1a 的 8 位大写十六进制。
func fnv1a32Hex(s string) string {
	const offset = uint32(2166136261)
	const prime = uint32(16777619)
	h := offset
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= prime
	}
	const digits = "0123456789ABCDEF"
	out := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		out[i] = digits[h&0xF]
		h >>= 4
	}
	return string(out)
}

// tokenEnd 返回从 off 开始的下一个记号（一个 rune，或 '%' 加两位十六进制）
// 之后的字节下标。
func tokenEnd(s string, off int) int {
	if s[off] == '%' && off+2 < len(s) &&
		isHex(s[off+1]) && isHex(s[off+2]) {
		return off + 3
	}
	_, size := utf8.DecodeRuneInString(s[off:])
	return off + size
}

func isHex(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'A' && b <= 'F'
}

// longestTokenPrefix 返回最长的记号前缀 P，使 len(P)+9 <= maxBytes。
func longestTokenPrefix(s string, maxBytes int) string {
	limit := maxBytes - 9 // len(P)+9 <= maxBytes
	end := 0
	for end < len(s) {
		next := tokenEnd(s, end)
		if next > limit {
			break
		}
		end = next
	}
	return s[:end]
}

// truncateStep 第四步：超长时按记号边界截断并附加 "~" + 8 位哈希。
func truncateStep(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	p := longestTokenPrefix(s, maxBytes)
	return p + "~" + fnv1a32Hex(s)
}

// baseMap 执行四步基础映射。调用前 src 已通过 validSrcName。
func baseMap(src string, maxBytes int) string {
	s := escapeStep(src)
	s = dotSpaceStep(s)
	s = reservedStep(s)
	s = truncateStep(s, maxBytes)
	return s
}

// splitBaseExt 以最后一个下标 > 0 的 '.' 拆分 t0。
func splitBaseExt(t0 string) (base, ext string) {
	i := strings.LastIndexByte(t0, '.')
	if i > 0 {
		return t0[:i], t0[i:]
	}
	return t0, ""
}

// shortenBaseTo 返回 base 的最长记号前缀，使 len(prefix) <= limit。
// 没有任何记号能放下时返回 (false)。
func shortenBaseTo(base string, limit int) (string, bool) {
	if len(base) <= limit {
		return base, true
	}
	end := 0
	for end < len(base) {
		next := tokenEnd(base, end)
		if next > limit {
			break
		}
		end = next
	}
	if end == 0 {
		return "", false
	}
	return base[:end], true
}
