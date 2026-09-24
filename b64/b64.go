// Package b64 提供 RFC 4648 标准字母表与单个 4 字符组的严格编解码。
package b64

// Alphabet 是 RFC 4648 标准 Base64 字母表。
const Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

// Pad 是填充字符。
const Pad = '='

var rev [256]int8

func init() {
	for i := range rev {
		rev[i] = -1
	}
	for i := 0; i < 64; i++ {
		rev[Alphabet[i]] = int8(i)
	}
}

// Sextet 返回字符 c 的 6-bit 值；c 不在字母表中时返回 -1。
func Sextet(c byte) int { return int(rev[c]) }

// Status 标识一个 4 字符组的解码结果类别。
type Status int

const (
	// OK 表示该组合法且为规范形式。
	OK Status = iota
	// BadChar 表示组内出现字母表与 '=' 之外的字符。
	BadChar
	// BadPadding 表示 '=' 出现在组内错误的位置。
	BadPadding
	// NonCanonical 表示尾部空闲比特非 0（非规范形式）。
	NonCanonical
)

// EncodeGroup 把 src（长度 1..3）编码为一个 4 字符组并追加到 dst。
func EncodeGroup(dst, src []byte) []byte {
	var v uint32
	for _, b := range src {
		v = v<<8 | uint32(b)
	}
	v <<= 8 * uint(3-len(src))
	for i := 0; i < len(src)+1; i++ {
		dst = append(dst, Alphabet[v>>18&0x3f])
		v <<= 6
	}
	for i := len(src) + 1; i < 4; i++ {
		dst = append(dst, Pad)
	}
	return dst
}

// DecodeGroup 严格解码一个完整的 4 字符组 g。返回载荷（1..3 字节）、
// 状态，以及与状态相关的组内下标（OK 时下标无意义）。
func DecodeGroup(g [4]byte) (out []byte, st Status, idx int) {
	var s [4]int
	for i, c := range g {
		switch {
		case c == Pad:
			s[i] = -1
		case Sextet(c) >= 0:
			s[i] = Sextet(c)
		default:
			return nil, BadChar, i
		}
	}
	if s[0] < 0 {
		return nil, BadPadding, 0
	}
	if s[1] < 0 {
		return nil, BadPadding, 1
	}
	n := 3
	switch {
	case s[3] >= 0:
		if s[2] < 0 {
			return nil, BadPadding, 2
		}
	case s[2] >= 0:
		n = 2
	default:
		n = 1
	}
	if n == 1 && s[1]&15 != 0 {
		return nil, NonCanonical, 1
	}
	if n == 2 && s[2]&3 != 0 {
		return nil, NonCanonical, 2
	}
	out = []byte{byte(s[0]<<2 | s[1]>>4)}
	if n >= 2 {
		out = append(out, byte(s[1]<<4|s[2]>>2))
	}
	if n == 3 {
		out = append(out, byte(s[2]<<6|s[3]))
	}
	return out, OK, 0
}
