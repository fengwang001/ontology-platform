// Package b64 实现 RFC 4648 标准 Base64 字母表与单个 4 字符组的严格编解码。
// 它不依赖其他包；流式处理由 stream 包负责。
package b64

import "errors"

// Alphabet 是 RFC 4648 的标准字母表。
const Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

var decodeTable [256]byte

func init() {
	for i := range decodeTable {
		decodeTable[i] = 0xFF
	}
	for i := 0; i < len(Alphabet); i++ {
		decodeTable[Alphabet[i]] = byte(i)
	}
}

// 组级错误：Idx 为出错字符在组内的下标（0..3），Idx<0 表示整体格式错误。
var (
	ErrChar    = errors.New("b64: illegal character")
	ErrPadding = errors.New("b64: padding position")
	ErrTail    = errors.New("b64: non-canonical tail")
)

// GroupError 携带组级错误类别与组内偏移。
type GroupError struct {
	Err error
	Idx int
}

func (e *GroupError) Error() string { return e.Err.Error() }
func (e *GroupError) Unwrap() error { return e.Err }

func bad(err error, idx int) error { return &GroupError{Err: err, Idx: idx} }

// Value 返回符号 c 的 6-bit 值；非法符号返回 (0xFF,false)。
func Value(c byte) (byte, bool) {
	v := decodeTable[c]
	return v, v != 0xFF
}

// IsData 报告 c 是否为字母表中的数据符号。
func IsData(c byte) bool {
	_, ok := Value(c)
	return ok
}

// DecodeGroup 严格解码一个恰含 4 个符号的组。
// g 中数据符号为 6-bit 值，填充槽为 0xFF。返回 1..3 个输出字节。
func DecodeGroup(g [4]byte) ([]byte, error) {
	pad := 0
	for i := 3; i >= 0 && g[i] == 0xFF; i-- {
	pad++
	}
	if pad > 2 || (pad == 1 && g[2] == 0xFF) || (pad == 2 && (g[1] == 0xFF || g[0] == 0xFF)) {
		off := -1
		for i, v := range g {
			if v == 0xFF {
				off = i
				break
			}
		}
		return nil, bad(ErrPadding, off)
	}
	u := uint32(g[0])<<18 | uint32(g[1])<<12 |
		uint32(tertiary(g[2]))<<6 | uint32(tertiary(g[3]))
	switch pad {
	case 0:
		return []byte{byte(u >> 16), byte(u >> 8), byte(u)}, nil
	case 1:
		if g[2]&0x3 != 0 {
			return nil, bad(ErrTail, 2)
		}
		return []byte{byte(u >> 16), byte(u >> 8)}, nil
	default:
		if g[1]&0xF != 0 {
			return nil, bad(ErrTail, 1)
		}
		return []byte{byte(u >> 16)}, nil
	}
}

func tertiary(v byte) byte {
	if v == 0xFF {
		return 0
	}
	return v
}

// EncodeGroup 把 1..3 字节编码为恰好 4 个符号；不足 3 字节时按规则补 '='。
func EncodeGroup(src []byte) [4]byte {
	var out [4]byte
	var u uint32
	for _, b := range src {
		u = u<<8 | uint32(b)
	}
	u <<= 8 * uint(3-len(src)) // 左移使数据顶到 24 位高端
	v := [4]byte{byte(u >> 18), byte(u >> 12), byte(u >> 6), byte(u)}
	for i := 0; i < 4; i++ {
		out[i] = '='
		if i < len(src)+1 {
			out[i] = Alphabet[v[i]&0x3F]
		}
	}
	return out
}
