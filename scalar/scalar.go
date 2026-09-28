// Package scalar 判定单个 Unicode 标量值与 UTF-8 首字节的合法结构。
// 它不依赖本工程的任何其他包，也不使用 unicode/utf8。
package scalar

// RuneSelf 与 unicode 包中的同名常量同义，这里独立给出。
const (
	RuneError = '\uFFFD' // 替换字符
	MaxRune   = '\U0010FFFF'
	SurrogateMin = 0xD800
	SurrogateMax = 0xDFFF
)

// IsScalar 报告 r 是否为 Unicode 标量值（非负、不超 U+10FFFF、非代理区）。
func IsScalar(r rune) bool {
	return r >= 0 && r <= MaxRune && (r < SurrogateMin || r > SurrogateMax)
}

// IsSurrogate 报告 r 是否落在代理区。
func IsSurrogate(r rune) bool { return r >= SurrogateMin && r <= SurrogateMax }

// IsHighSurrogate / IsLowSurrogate 判定 UTF-16 代理类别。
func IsHighSurrogate(r rune) bool { return r >= 0xD800 && r <= 0xDBFF }
func IsLowSurrogate(r rune) bool  { return r >= 0xDC00 && r <= 0xDFFF }

// IsCont 报告 b 是否为 UTF-8 续字节 10xxxxxx。
func IsCont(b byte) bool { return b&0xC0 == 0x80 }

// LeadInfo 描述 UTF-8 首字节 lead 的结构：
// size 为该字符总字节数（1..4）；lo/hi 为第二字节（若有）的闭区间；
// ok 为 false 表示 lead 永远不可能开始一个合法字符（80..BF、C0、C1、F5..FF）。
func LeadInfo(lead byte) (size int, lo, hi byte, ok bool) {
	switch {
	case lead < 0x80:
		return 1, 0, 0, true
	case lead >= 0xC2 && lead <= 0xDF:
		return 2, 0x80, 0xBF, true
	case lead == 0xE0:
		return 3, 0xA0, 0xBF, true
	case lead >= 0xE1 && lead <= 0xEC:
		return 3, 0x80, 0xBF, true
	case lead == 0xED:
		return 3, 0x80, 0x9F, true
	case lead >= 0xEE && lead <= 0xEF:
		return 3, 0x80, 0xBF, true
	case lead == 0xF0:
		return 4, 0x90, 0xBF, true
	case lead >= 0xF1 && lead <= 0xF3:
		return 4, 0x80, 0xBF, true
	case lead == 0xF4:
		return 4, 0x80, 0x8F, true
	default: // 80..BF、C0、C1、F5..FF
		return 0, 0, 0, false
}

// DecodeCont 把续字节 b 追加进累积值 cp。
func DecodeCont(cp rune, b byte) rune { return cp<<6 | rune(b&0x3F) }

// Encode 把标量 r 编码成 UTF-8，返回其字节；调用方须先用 IsScalar 校验。
func Encode(r rune) []byte {
	switch {
	case r < 0x80:
		return []byte{byte(r)}
	case r < 0x800:
		return []byte{0xC0 | byte(r>>6), 0x80 | byte(r)&0x3F}
	case r < 0x10000:
		return []byte{
			0xE0 | byte(r>>12),
			0x80 | byte(r>>6)&0x3F,
			0x80 | byte(r)&0x3F,
		}
	default:
		return []byte{
			0xF0 | byte(r>>18),
			0x80 | byte(r>>12)&0x3F,
			0x80 | byte(r>>6)&0x3F,
			0x80 | byte(r)&0x3F,
		}
	}
}
