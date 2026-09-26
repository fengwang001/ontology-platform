// Package enc 实现单个 rune 的 UTF-8 严格编解码，不依赖其他包。
package enc

import "errors"

// 六类非法输入的哨兵错误，互不相同，可用 errors.Is 判定。
var (
	ErrInvalidLead     = errors.New("enc: invalid lead byte")
	ErrOverlong        = errors.New("enc: overlong encoding")
	ErrSurrogate       = errors.New("enc: surrogate code point")
	ErrOutOfRange      = errors.New("enc: code point above U+10FFFF")
	ErrTruncated       = errors.New("enc: truncated sequence")
	ErrBadContinuation = errors.New("enc: invalid continuation byte")
)

const maxRune = 0x10FFFF

// n 字节序列能合法表示的最小码点：小于它即为过长编码。
var minFor = [5]rune{0, 0x0, 0x80, 0x800, 0x10000}

// EncodeRune 把 r 编成 UTF-8 字节序列；代理项与越界码点被拒绝。
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
		return []byte{0xF0 | byte(r>>18), 0x80 | byte(r>>12)&0x3F, 0x80 | byte(r>>6)&0x3F, 0x80 | byte(r)&0x3F}, nil
	}
}

// DecodeRune 从 b 开头解一个 rune，返回 (码点, 消耗字节数, 错误)。
// 每个字节只读一次；过长/代理项/越界在拼出的码点上做范围判定，不重读字节。
func DecodeRune(b []byte) (rune, int, error) {
	if len(b) == 0 {
		return 0, 0, ErrTruncated
	}
	b0 := b[0]
	var n int   // 序列总长
	var cp rune // 首字节贡献的码点位
	switch {
	case b0 < 0x80:
		return rune(b0), 1, nil
	case b0 < 0xC0: // 0x80..0xBF：续字节不能当首字节
		return 0, 0, ErrInvalidLead
	case b0 < 0xE0:
		n, cp = 2, rune(b0&0x1F)
	case b0 < 0xF0:
		n, cp = 3, rune(b0&0x0F)
	case b0 < 0xF8:
		n, cp = 4, rune(b0&0x07)
	default: // 0xF8..0xFF
		return 0, 0, ErrInvalidLead
	}
	if len(b) < n {
		return 0, 0, ErrTruncated
	}
	for i := 1; i < n; i++ {
		c := b[i]
		if c&0xC0 != 0x80 {
			return 0, 0, ErrBadContinuation
		}
		cp = cp<<6 | rune(c&0x3F)
	}
	switch {
	case cp < minFor[n]:
		return 0, 0, ErrOverlong
	case cp >= 0xD800 && cp <= 0xDFFF:
		return 0, 0, ErrSurrogate
	case cp > maxRune:
		return 0, 0, ErrOutOfRange
	}
	return cp, n, nil
}
