// Package b64 实现标准字母表 Base64 编解码（含严格校验）。不依赖其他包。
package b64

import "errors"

// 可判定的哨兵错误。
var (
	ErrLength       = errors.New("b64: 长度不是 4 的倍数")
	ErrPadding      = errors.New("b64: = 位置或数量非法")
	ErrInvalidChar  = errors.New("b64: 非法字符")
	ErrTrailingBits = errors.New("b64: 末尾未用位非零")
)

// alphabet 下标 0..63：A-Z、a-z、0-9、+、/。
const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

// rev 是字母表的反查表，非法字符为 -1。
var rev = func() (t [256]int8) {
	for i := range t {
		t[i] = -1
	}
	for i := 0; i < 64; i++ {
		t[alphabet[i]] = int8(i)
	}
	return
}()

// Encode 把字节串编成 Base64（标准字母表 + 填充）。空输入得空串。
func Encode(src []byte) []byte {
	dst := make([]byte, 0, (len(src)+2)/3*4)
	for len(src) > 0 {
		n := min(3, len(src))
		dst = encodeBlock(dst, src[:n])
		src = src[n:]
	}
	return dst
}

// encodeBlock 把 1..3 个字节编成 4 个字符（含填充）追加到 dst。
func encodeBlock(dst, src []byte) []byte {
	var b [3]byte
	copy(b[:], src)
	v := uint32(b[0])<<16 | uint32(b[1])<<8 | uint32(b[2])
	dst = append(dst, alphabet[v>>18&63], alphabet[v>>12&63])
	if len(src) > 1 {
		dst = append(dst, alphabet[v>>6&63])
	} else {
		dst = append(dst, '=')
	}
	if len(src) > 2 {
		dst = append(dst, alphabet[v&63])
	} else {
		dst = append(dst, '=')
	}
	return dst
}

// Decode 严格解码：任一校验不满足即整体失败，返回 (nil, error)。
func Decode(src []byte) ([]byte, error) {
	if len(src) == 0 {
		return []byte{}, nil
	}
	if len(src)%4 != 0 {
		return nil, ErrLength
	}
	pad := 0
	for pad < len(src) && src[len(src)-1-pad] == '=' {
		pad++
	}
	if pad > 2 {
		return nil, ErrPadding
	}
	for i := 0; i < len(src)-pad; i++ {
		if src[i] == '=' {
			return nil, ErrPadding
		}
	}
	out := make([]byte, 0, len(src)/4*3)
	for i := 0; i < len(src); i += 4 {
		blk, err := decodeBlock(src[i : i+4])
		if err != nil {
			return nil, err
		}
		out = append(out, blk...)
	}
	return out, nil
}

// decodeBlock 解一个 4 字符块，校验字符与末尾未用位，返回 1..3 字节。
// 调用方须保证 '=' 只出现在块尾且至多 2 个。
func decodeBlock(b []byte) ([]byte, error) {
	pad := 0
	for pad < 4 && b[3-pad] == '=' {
		pad++
	}
	var v [4]uint32
	for i := 0; i < 4-pad; i++ {
		d := rev[b[i]]
		if d < 0 {
			return nil, ErrInvalidChar
		}
		v[i] = uint32(d)
	}
	switch pad {
	case 1:
		if v[2]&3 != 0 {
			return nil, ErrTrailingBits
		}
	case 2:
		if v[1]&15 != 0 {
			return nil, ErrTrailingBits
		}
	}
	n := v[0]<<18 | v[1]<<12 | v[2]<<6 | v[3]
	out := []byte{byte(n >> 16)}
	if pad < 2 {
		out = append(out, byte(n>>8))
	}
	if pad < 1 {
		out = append(out, byte(n))
	}
	return out, nil
}
