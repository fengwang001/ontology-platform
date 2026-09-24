// Package b32 实现 base32 编解码：按 5 字节分组，每组产生 8 个字符。
package b32

import (
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	_ "unsafe" // 供 linkname 标记计数器

	"ontology/alpha"
)

// 三类互不相同的可判定解码错误。
var (
	ErrInvalidChar     = errors.New("b32: character not in alphabet")
	ErrPaddingPosition = errors.New("b32: padding in non-final position")
	ErrPaddingCount    = errors.New("b32: illegal padding count")
)

// groups 记录编解码处理的 5 字节分组数，非导出，不出现在公开接口。
//
//go:linkname groups
var groups atomic.Int64

// Encode 把 src 编码为 base32 字符串，长度恰为 8*⌈n/5⌉。
func Encode(src []byte) string {
	var sb strings.Builder
	sb.Grow((len(src) + 4) / 5 * 8)
	for i := 0; i < len(src); i += 5 {
		groups.Add(1)
		var block [5]byte
		n := copy(block[:], src[i:])
		v := uint64(block[0])<<32 | uint64(block[1])<<24 |
			uint64(block[2])<<16 | uint64(block[3])<<8 | uint64(block[4])
		for j := 0; j < 8; j++ {
			if j < (n*8+4)/5 {
				sb.WriteByte(alpha.Char(byte(v>>uint(35-5*j)) & 31))
			} else {
				sb.WriteByte('=')
			}
		}
	}
	return sb.String()
}

// Decode 解码 s。任何非法输入都整体失败：返回 nil 与可判定错误，不改入参。
func Decode(s string) ([]byte, error) {
	if len(s)%8 != 0 {
		return nil, fmt.Errorf("%w: length %d at index %d", ErrPaddingCount, len(s), len(s))
	}
	pad := 0
	for pad < len(s) && s[len(s)-1-pad] == '=' {
		pad++
	}
	switch pad {
	case 0, 1, 3, 4, 6: // 合法填充集合，见 NOTES.md
	default:
		return nil, fmt.Errorf("%w: %d at index %d", ErrPaddingCount, pad, len(s)-pad)
	}
	body := len(s) - pad
	for i := 0; i < body; i++ {
		if s[i] == '=' {
			return nil, fmt.Errorf("%w at index %d", ErrPaddingPosition, i)
		}
		if !alpha.Valid(s[i]) {
			return nil, fmt.Errorf("%w %q at index %d", ErrInvalidChar, s[i], i)
		}
	}
	out := make([]byte, 0, len(s)/8*5)
	for i := 0; i < len(s); i += 8 {
		groups.Add(1)
		chars := 8
		if body-i < 8 {
			chars = body - i
		}
		var v uint64
		for j := 0; j < chars; j++ {
			v = v<<5 | uint64(alpha.Value(s[i+j]))
		}
		v <<= uint(5 * (8 - chars))
		for j := 0; j < chars*5/8; j++ {
			out = append(out, byte(v>>uint(32-8*j)))
		}
	}
	return out, nil
}
