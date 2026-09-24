// Package b64 实现 RFC 4648 标准字母表上、单个 4 字符组的编解码与合法性判定。
// 本包不依赖项目内任何其他包，也不使用 encoding/base64。
package b64

import "errors"

// Alphabet 为 RFC 4648 标准 Base64 字母表（不含填充符）。
const Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

// Pad 为填充字符。
const Pad = '='

var (
	// ErrIllegalChar 表示出现字母表与填充符之外的字符。
	ErrIllegalChar = errors.New("b64: illegal base64 character")
	// ErrPadding 表示一个 4 字符组内填充的位置/形态不合法。
	ErrPadding = errors.New("b64: illegal padding placement")
	// ErrLength 表示输入长度不是 4。
	ErrLength = errors.New("b64: group length is not 4")
	// ErrNonCanonical 表示最后一个含填充组的尾随比特非零（非规范尾部）。
	ErrNonCanonical = errors.New("b64: non-canonical trailing bits")
)

// Kind 描述一个 4 字符组的形态。
type Kind int

const (
	// KindFull 为无填充满组，输出 3 字节。
	KindFull Kind = iota
	// KindOne 为 xx== 形态，输出 1 字节。
	KindOne
	// KindTwo 为 xxx= 形态，输出 2 字节。
	KindTwo
)

var decodeTable [256]byte

func init() {
	for i := range decodeTable {
		decodeTable[i] = 0xFF
	}
	for i := 0; i < len(Alphabet); i++ {
	decodeTable[Alphabet[i]] = byte(i)
}
}

// IsData 报告 b 是否为字母表字符。
func IsData(b byte) bool { return decodeTable[b] != 0xFF }

// Value 返回字母表字符的 6 比特值；非字母表字符返回 (0,false)。
func Value(b byte) (byte, bool) {
	v := decodeTable[b]
	return v, v != 0xFF
}

// DecodeGroup 解码恰好 4 个字符的一个组，返回组形态与输出字节（1~3 字节）。
// 非法字符返回 ErrIllegalChar（index 为组内偏移）；
// 填充形态错误返回 ErrPadding；尾随比特非零返回 ErrNonCanonical。
func DecodeGroup(g [4]byte) (Kind, []byte, int, error) {
	var v [4]byte
	for i := 0; i < 4; i++ {
		if val, ok := Value(g[i]); ok {
			v[i] = val
			continue
		}
		if g[i] != Pad {
			return 0, nil, i, ErrIllegalChar
		}
		switch i {
		case 0, 1:
			return 0, nil, i, ErrPadding
		case 2:
			if g[3] != Pad {
				return 0, nil, 3, ErrPadding
			}
			if v[1]&0x0F != 0 {
				return KindOne, nil, 1, ErrNonCanonical
			}
			out := []byte{v[0]<<2 | v[1]>>4}
			return KindOne, out, -1, nil
		case 3:
			if v[2]&0x03 != 0 {
				return KindTwo, nil, 2, ErrNonCanonical
			}
			out := []byte{v[0]<<2 | v[1]>>4, (v[1]&0x0F)<<4 | v[2]>>2}
			return KindTwo, out, -1, nil
		}
	}
	out := []byte{
		v[0]<<2 | v[1]>>4,
		(v[1]&0x0F)<<4 | v[2]>>2,
		(v[2]&0x03)<<6 | v[3],
	}
	return KindFull, out, -1, nil
}

// EncodeGroup 将 1~3 字节编码为一个 4 字符组（按需补 =）。
func EncodeGroup(src []byte) ([4]byte, error) {
	var g [4]byte
	switch len(src) {
	case 1:
		g[0] = Alphabet[src[0]>>2]
		g[1] = Alphabet[(src[0]&0x03)<<4]
		g[2], g[3] = Pad, Pad
	case 2:
		g[0] = Alphabet[src[0]>>2]
		g[1] = Alphabet[(src[0]&0x03)<<4|src[1]>>4]
		g[2] = Alphabet[(src[1]&0x0F)<<2]
		g[3] = Pad
	case 3:
		g[0] = Alphabet[src[0]>>2]
		g[1] = Alphabet[(src[0]&0x03)<<4|src[1]>>4]
		g[2] = Alphabet[(src[1]&0x0F)<<2|src[2]>>6]
		g[3] = Alphabet[src[2]&0x3F]
	default:
		return g, ErrLength
	}
	return g, nil
}
