// Package scalar 判定单个 Unicode 标量值，不依赖其他包。
package scalar

const (
	MaxRune   = 0x10FFFF // 标量值上界
	Surrogate = 0xD800   // 代理区起点
	SurrEnd   = 0xDFFF   // 代理区终点
	Replacement = 0xFFFD // 替换字符
)

// Valid 报告 r 是否为标量值（非代理、不越界）。
func Valid(r rune) bool {
	return r >= 0 && r < Surrogate || r > SurrEnd && r <= MaxRune
}

// Surrogate 报告 r 是否落在代理区。
func IsSurrogate(r rune) bool { return r >= Surrogate && r <= SurrEnd }

// Append 将标量值 r 追加为 UTF-8 字节；非法 r 追加一个替换字符。
func Append(p []byte, r rune) []byte {
	if !Valid(r) {
		r = Replacement
	}
	switch {
	case r < 0x80:
		return append(p, byte(r))
	case r < 0x800:
		return append(p, 0xC0|byte(r>>6), 0x80|byte(r)&0x3F)
	case r < 0x10000:
		return append(p, 0xE0|byte(r>>12), 0x80|byte(r>>6)&0x3F, 0x80|byte(r)&0x3F)
	default:
		return append(p, 0xF0|byte(r>>18), 0x80|byte(r>>12)&0x3F,
			0x80|byte(r>>6)&0x3F, 0x80|byte(r)&0x3F)
	}
}

// Len 返回 r 的 UTF-8 编码字节数。
func Len(r rune) int {
	switch {
	case r < 0x80:
		return 1
	case r < 0x800:
		return 2
	case r < 0x10000:
		return 3
	default:
		return 4
	}
}
