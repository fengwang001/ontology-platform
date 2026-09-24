// Package b64 提供 RFC 4648 标准字母表与单个 4 字符组的严格编解码。
package b64

import "errors"

// Alphabet 是 RFC 4648 标准 Base64 字母表。
const Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

// 组级错误哨兵，stream 包会为其附加整个输入流中的字节偏移。
var (
	ErrChar      = errors.New("b64: invalid character")
	ErrPadding   = errors.New("b64: misplaced padding")
	ErrCanonical = errors.New("b64: non-canonical trailing bits")
)

var rev [256]int16

func init() {
	for i := range rev {
		rev[i] = -1
	}
	for i := 0; i < len(Alphabet); i++ {
		rev[Alphabet[i]] = int16(i)
	}
}

// Value 返回字符 c 的 6 比特值；c 不在字母表中时返回 -1。
func Value(c byte) int {
	return int(rev[c])
}

// EncodeGroup 把 1~3 个字节编码为 4 个字符（按需补 '='），写入 dst，返回 4。
func EncodeGroup(src, dst []byte) int {
	var n uint32
	for _, b := range src {
		n = n<<8 | uint32(b)
	}
	n <<= 8 * uint(3-len(src))
	dst[0] = Alphabet[n>>18&63]
	dst[1] = Alphabet[n>>12&63]
	dst[2] = '='
	dst[3] = '='
	if len(src) > 1 {
		dst[2] = Alphabet[n>>6&63]
	}
	if len(src) > 2 {
		dst[3] = Alphabet[n&63]
	}
	return 4
}

// DecodeGroup 严格解码一个 4 字符组，把 1~3 个字节写入 dst。
// 返回写入的字节数；出错时返回出错字符在组内的下标与错误类别。
func DecodeGroup(g [4]byte, dst []byte) (int, int, error) {
	v := [4]int{-1, -1, -1, -1}
	for i := 0; i < 4; i++ {
		if g[i] == '=' {
			if i < 2 {
				return 0, i, ErrPadding
			}
			continue
		}
		v[i] = Value(g[i])
		if v[i] < 0 {
			return 0, i, ErrChar
		}
	}
	switch {
	case g[2] == '=':
		if g[3] != '=' {
			return 0, 2, ErrPadding
		}
		if v[1]&0x0F != 0 { // XX== 的未用比特：第 2 字符低 4 位
			return 0, 1, ErrCanonical
		}
		dst[0] = byte(v[0]<<2 | v[1]>>4)
		return 1, -1, nil
	case g[3] == '=':
		if v[2]&0x03 != 0 { // XXX= 的未用比特：第 3 字符低 2 位
			return 0, 2, ErrCanonical
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
