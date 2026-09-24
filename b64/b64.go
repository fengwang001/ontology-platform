// Package b64 提供 RFC 4648 标准字母表与单个 4 字符组的严格编解码。
package b64

import "errors"

// Std 是 RFC 4648 标准字母表。
const Std = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

// 组级合法性错误，stream 包会附上字节偏移重新包装。
var (
	ErrChar  = errors.New("b64: invalid character")
	ErrCanon = errors.New("b64: non-canonical trailing bits")
	ErrPad   = errors.New("b64: misplaced padding")
)

var dec [256]int8

func init() {
	for i := range dec {
		dec[i] = -1
	}
	for i := 0; i < 64; i++ {
		dec[Std[i]] = int8(i)
	}
}

// Val 返回 c 的 6 bit 值；非字母表字符返回 -1。
func Val(c byte) int { return int(dec[c]) }

// AppendGroup 把 src（1~3 字节）编码为一个 4 字符组追加到 dst，按需补 =。
func AppendGroup(dst []byte, src []byte) []byte {
	var n uint32
	for i := 0; i < 3; i++ {
		n <<= 8
		if i < len(src) {
			n |= uint32(src[i])
		}
	}
	for i := 0; i < 4; i++ {
		if i <= len(src) {
			dst = append(dst, Std[(n>>uint(18-6*i))&63])
		} else {
			dst = append(dst, '=')
		}
	}
	return dst
}

// DecodeGroup 严格解码一个完整的 4 字符组，返回解码字节与有效长度。
// 出错时 bad 给出组内出错字符的下标，err 为上述哨兵错误之一。
func DecodeGroup(g [4]byte) (out [3]byte, n, bad int, err error) {
	var v [4]int
	pad := 0
	for i := 0; i < 4; i++ {
		c := g[i]
		if c == '=' {
			if i < 2 {
				return out, 0, i, ErrPad
			}
			pad++
			continue
		}
		if pad > 0 {
			return out, 0, i, ErrPad
		}
		if dec[c] < 0 {
			return out, 0, i, ErrChar
		}
		v[i] = int(dec[c])
	}
	switch pad {
	case 0:
		n = 3
	case 1:
		if v[2]&3 != 0 { // XXX=：第 3 个六比特组低 2 bit 必须为零
			return out, 0, 2, ErrCanon
		}
		n = 2
	case 2:
		if v[1]&15 != 0 { // XX==：第 2 个六比特组低 4 bit 必须为零
			return out, 0, 1, ErrCanon
		}
		n = 1
	default:
		return out, 0, 2, ErrPad
	}
	bits := v[0]<<18 | v[1]<<12 | v[2]<<6 | v[3]
	out[0], out[1], out[2] = byte(bits>>16), byte(bits>>8), byte(bits)
	return out, n, -1, nil
}
