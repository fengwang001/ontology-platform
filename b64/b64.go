// Package b64 实现 RFC 4648 标准字母表的组级编解码与严格合法性判定。
package b64

import "errors"

// Alphabet 是标准 Base64 字母表，Pad 是填充字符。
const Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

const Pad = '='

// 三类可区分的组级错误。
var (
	ErrInvalidChar  = errors.New("b64: invalid character")
	ErrNonCanonical = errors.New("b64: non-canonical trailing bits")
	ErrBadPadding   = errors.New("b64: misplaced padding")
)

var rev [256]int8

func init() {
	for i := range rev {
		rev[i] = -1
	}
	for i := 0; i < len(Alphabet); i++ {
		rev[Alphabet[i]] = int8(i)
	}
}

// Value 返回字母表字符 c 的 6 bit 值；ok 为 false 表示 c 不在字母表中。
func Value(c byte) (v int, ok bool) {
	if rev[c] < 0 {
		return 0, false
	}
	return int(rev[c]), true
}

// Classify 返回字符 c 的 6 bit 值；Pad 返回 -1；ok 为 false 表示非法字符。
func Classify(c byte) (v int, ok bool) {
	if c == Pad {
		return -1, true
	}
	return Value(c)
}

// EncodeGroup 把 src（1~3 字节）编码为 4 个字符，按需补 Pad。
func EncodeGroup(src []byte) (dst [4]byte) {
	var b [3]byte
	n := copy(b[:], src)
	dst[0] = Alphabet[b[0]>>2]
	dst[1] = Alphabet[b[0]&0x03<<4|b[1]>>4]
	dst[2], dst[3] = Pad, Pad
	if n > 1 {
		dst[2] = Alphabet[b[1]&0x0f<<2|b[2]>>6]
	}
	if n > 2 {
		dst[3] = Alphabet[b[2]&0x3f]
	}
	return
}

// DecodeGroup 严格解码一个完整 4 字符组 g，把 1~3 字节写入 dst 并返回字节数。
// 出错时 bad 为组内出错下标，err 为上面三类哨兵错误之一。
func DecodeGroup(g [4]byte, dst []byte) (n, bad int, err error) {
	var v [4]int
	for i := 0; i < 4; i++ {
		if g[i] == Pad {
			v[i] = -1
		} else if val, ok := Value(g[i]); ok {
			v[i] = val
		} else {
			return 0, i, ErrInvalidChar
		}
	}
	return DecodeVals(v, dst)
}

// DecodeVals 严格解码一组 4 个 6 bit 值（-1 表示 Pad），供流式解码器复用。
// 规范形式：尾组 xx== 要求第 2 字符低 4 bit 为 0；xxx= 要求第 3 字符低 2 bit 为 0。
func DecodeVals(v [4]int, dst []byte) (n, bad int, err error) {
	if v[0] < 0 {
		return 0, 0, ErrBadPadding
	}
	if v[1] < 0 {
		return 0, 1, ErrBadPadding
	}
	switch {
	case v[2] < 0:
		if v[3] >= 0 {
			return 0, 2, ErrBadPadding
		}
		if v[1]&0x0f != 0 {
			return 0, 1, ErrNonCanonical
		}
		dst[0] = byte(v[0]<<2 | v[1]>>4)
		return 1, -1, nil
	case v[3] < 0:
		if v[2]&0x03 != 0 {
			return 0, 2, ErrNonCanonical
		}
		dst[0] = byte(v[0]<<2 | v[1]>>4)
		dst[1] = byte(v[1]<<4 | v[2]>>2)
		return 2, -1, nil
	default:
		dst[0] = byte(v[0]<<2 | v[1]>>4)
		dst[1] = byte(v[1]<<4 | v[2]>>2)
		dst[2] = byte(v[2]<<6 | v[3])
		return 3, -1, nil
	}
}
