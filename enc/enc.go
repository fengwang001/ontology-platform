// Package enc 实现单个 rune 的 UTF-8 严格编解码。
package enc

import "errors"

var (
	ErrBadLead         = errors.New("enc: 非法首字节")
	ErrOverlong        = errors.New("enc: 过长编码")
	ErrSurrogate       = errors.New("enc: 代理项码点")
	ErrOutOfRange      = errors.New("enc: 码点越界")
	ErrTruncated       = errors.New("enc: 续字节不足（截断）")
	ErrBadContinuation = errors.New("enc: 非法续字节")
)

const maxRune = 0x10FFFF

// 每种序列长度允许的最小码点，用于过长编码判定（无需重编码对照）。
var minForLen = [5]rune{0, 0x0, 0x80, 0x800, 0x10000}

// EncodeRune 把 r 编成 UTF-8；代理项与越界码点被拒绝。
func EncodeRune(r rune) ([]byte, error) {
	switch {
	case r >= 0xD800 && r <= 0xDFFF:
		return nil, ErrSurrogate
	case r < 0 || r > maxRune:
		return nil, ErrOutOfRange
	case r < 0x80:
		return []byte{byte(r)}, nil
	case r < 0x800:
		return []byte{0xC0 | byte(r>>6), 0x80 | byte(r)&0x3F}, nil
	case r < 0x10000:
		return []byte{0xE0 | byte(r>>12), 0x80 | byte(r>>6)&0x3F, 0x80 | byte(r)&0x3F}, nil
	default:
		return []byte{0xF0 | byte(r>>18), 0x80 | byte(r>>12)&0x3F,
			0x80 | byte(r>>6)&0x3F, 0x80 | byte(r)&0x3F}, nil
	}
}

// DecodeRune 解 b 开头的一个 rune，返回 (码点, 消耗字节数, 错误)。
// 单趟完成：边读边拼码点，长度已知后用 minForLen 判过长，不做重编码对照。
func DecodeRune(b []byte) (rune, int, error) {
	if len(b) == 0 {
		return 0, 0, ErrTruncated
	}
	b0 := b[0]
	var size int
	var r rune
	switch {
	case b0 < 0x80:
		return rune(b0), 1, nil
	case b0 < 0xC0: // 0x80..0xBF：续字节出现在首位
		return 0, 0, ErrBadLead
	case b0 < 0xE0:
		size, r = 2, rune(b0&0x1F)
	case b0 < 0xF0:
		size, r = 3, rune(b0&0x0F)
	case b0 < 0xF8:
		size, r = 4, rune(b0&0x07)
	default: // 0xF8..0xFF
		return 0, 0, ErrBadLead
	}
	if len(b) < size {
		return 0, 0, ErrTruncated
	}
	for i := 1; i < size; i++ {
		c := b[i]
		if c&0xC0 != 0x80 {
			return 0, 0, ErrBadContinuation
		}
		r = r<<6 | rune(c&0x3F)
	}
	switch {
	case r < minForLen[size]:
		return 0, 0, ErrOverlong
	case r >= 0xD800 && r <= 0xDFFF:
		return 0, 0, ErrSurrogate
	case r > maxRune:
		return 0, 0, ErrOutOfRange
	}
	return r, size, nil
}
