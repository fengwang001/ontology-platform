// Package b64 提供 RFC 4648 标准字母表与单个 4 字符组的严格编解码。
package b64

import "errors"

// Alphabet 是 RFC 4648 标准 Base64 字母表。
const Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

// ErrTail 表示尾部组的补位比特不全为 0（非规范形式）。
var ErrTail = errors.New("b64: 非规范尾部")

var decodeTab [256]int8

func init() {
	for i := range decodeTab {
		decodeTab[i] = -1
	}
	for i := 0; i < len(Alphabet); i++ {
		decodeTab[Alphabet[i]] = int8(i)
	}
}

// Value 返回字符 c 的 6-bit 值；c 不在字母表中时 ok 为 false。
func Value(c byte) (v byte, ok bool) {
	if n := decodeTab[c]; n >= 0 {
		return byte(n), true
	}
	return 0, false
}

// IsPad 报告 c 是否为填充符 '='。
func IsPad(c byte) bool { return c == '=' }

// Assemble 把一个组的 4 个 6-bit 值（pads 个尾部填充）组装成 3-pads 个字节，
// 并校验规范尾部：1 字节尾部查 v[1] 低 4 bit，2 字节尾部查 v[2] 低 2 bit。
func Assemble(v [4]byte, pads int, out *[3]byte) (int, error) {
	switch pads {
	case 0:
		out[0] = v[0]<<2 | v[1]>>4
		out[1] = v[1]<<4 | v[2]>>2
		out[2] = v[2]<<6 | v[3]
		return 3, nil
	case 1:
		if v[2]&0b11 != 0 {
			return 0, ErrTail
		}
		out[0] = v[0]<<2 | v[1]>>4
		out[1] = v[1]<<4 | v[2]>>2
		return 2, nil
	default: // pads == 2
		if v[1]&0b1111 != 0 {
			return 0, ErrTail
		}
		out[0] = v[0]<<2 | v[1]>>4
		return 1, nil
	}
}

// Encode 把 src 编码为规范 Base64（按需补 '='），追加到 dst 后返回。
func Encode(dst, src []byte) []byte {
	for len(src) >= 3 {
		n := uint32(src[0])<<16 | uint32(src[1])<<8 | uint32(src[2])
		dst = append(dst, Alphabet[n>>18], Alphabet[n>>12&63], Alphabet[n>>6&63], Alphabet[n&63])
		src = src[3:]
	}
	switch len(src) {
	case 1:
		n := uint32(src[0]) << 16
		dst = append(dst, Alphabet[n>>18], Alphabet[n>>12&63], '=', '=')
	case 2:
		n := uint32(src[0])<<16 | uint32(src[1])<<8
		dst = append(dst, Alphabet[n>>18], Alphabet[n>>12&63], Alphabet[n>>6&63], '=')
	}
	return dst
}
