// Package u8 手写 UTF-8 的逐标量解码与编码，不使用 unicode/utf8。
package u8

import "ontology/scalar"

// BOM 是 UTF-8 字节序标记。
var BOM = []byte{0xEF, 0xBB, 0xBF}

// RuneError 是替换字符 U+FFFD。
const RuneError rune = 0xFFFD

// LeadLen 返回 UTF-8 首字节声明的序列长度；非首字节（延续/永非法/ASCII）返回 0/1 区分：
// ASCII 与永非法、孤立延续返回 1（各自独立成单元），合法多字节首字节返回 2/3/4。
func LeadLen(b byte) int {
	switch {
	case b < 0x80:
		return 1
	case b >= 0xC2 && b <= 0xDF:
		return 2
	case b >= 0xE0 && b <= 0xEF:
		return 3
	case b >= 0xF0 && b <= 0xF4:
		return 4
	default:
		return 1
	}
}

// Decode 从 b 的首字节解码一个完整单元（b 需含其后所有可用字节）。
// valid=true 时 r 为标量、size 为字节数；valid=false 且 more=true 表示字节尚未到齐；
// valid=false 且 more=false 时 size 为该非法单元吞掉的字节数。
func Decode(b []byte) (r rune, size int, valid, more bool) {
	if len(b) == 0 {
		return 0, 0, false, true
	}
	lead := b[0]
	n := LeadLen(lead)
	if n == 1 {
		if lead < 0x80 {
			return rune(lead), 1, true, false
		}
		return 0, 1, false, false
	}
	if len(b) < 2 || !scalar.SecondOK(lead, b[1]) {
		if len(b) < 2 {
			return 0, len(b), false, true
		}
		return 0, 1, false, false
	}
	i := 2
	for i < n {
		if i >= len(b) {
			return 0, i, false, true
		}
		if !scalar.Cont(b[i]) {
			return 0, i, false, false
		}
		i++
	}
	switch n {
	case 2:
		r = rune(lead&0x1F)<<6 | rune(b[1]&0x3F)
	case 3:
		r = rune(lead&0x0F)<<12 | rune(b[1]&0x3F)<<6 | rune(b[2]&0x3F)
	case 4:
		r = rune(lead&0x07)<<18 | rune(b[1]&0x3F)<<12 | rune(b[2]&0x3F)<<6 | rune(b[3]&0x3F)
	}
	if !scalar.IsScalar(r) {
		return 0, n, false, false
	}
	return r, n, true, false
}

// Encode 把标量编码成 UTF-8；非 Scalar 按 U+FFFD 编码。
func Encode(r rune) []byte {
	if !scalar.IsScalar(r) {
		r = RuneError
	}
	switch {
	case r < 0x80:
		return []byte{byte(r)}
	case r < 0x800:
		return []byte{0xC0 | byte(r>>6), 0x80 | byte(r&0x3F)}
	case r < 0x10000:
		return []byte{0xE0 | byte(r>>12), 0x80 | byte(r>>6&0x3F), 0x80 | byte(r&0x3F)}
	default:
		return []byte{
			0xF0 | byte(r>>18),
			0x80 | byte(r>>12&0x3F),
			0x80 | byte(r>>6&0x3F),
			0x80 | byte(r&0x3F),
		}
	}
}

// AnchorAt 报告 b[i] 是否可作为 UTF-8 解析起点（ASCII 或多字节首字节）。
func AnchorAt(b []byte, i int) bool {
	return b[i] < 0x80 || b[i] >= 0xC0
}

// SplitStart 返回内部段（原始切点 c）真正应开始解析的绝对偏移：
// 回退到最近锚点；若该锚点单元在 c 之前已完整结束则从 c 开始，否则整单元让给本段。
func SplitStart(b []byte, c int) int {
	if c <= 0 {
		return 0
	}
	a := c - 1
	for a > c-3 && !AnchorAt(b, a) {
		a--
	}
	if !AnchorAt(b, a) {
		return c
	}
	_, size, _, more := Decode(b[a:])
	if more || a+size > c {
		return a
	}
	return c
}
