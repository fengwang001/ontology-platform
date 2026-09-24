// Package b64 implements the RFC 4648 standard alphabet and the codec
// for a single 4-character group, including canonical-form checks.
package b64

import "errors"

var (
	ErrInvalidChar  = errors.New("b64: invalid character")
	ErrPadding      = errors.New("b64: misplaced padding")
	ErrNonCanonical = errors.New("b64: non-canonical trailing bits")
)

const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

var rev [256]int8

func init() {
	for i := range rev {
		rev[i] = -1
	}
	for i := 0; i < 64; i++ {
		rev[alphabet[i]] = int8(i)
	}
}

// Char returns the alphabet character for the 6-bit value v.
func Char(v int) byte { return alphabet[v&63] }

// Value returns the 6-bit value of c, or -1 if c is not in the alphabet.
func Value(c byte) int { return int(rev[c]) }

// EncodeGroup encodes 1 to 3 source bytes into a 4-character group,
// appending '=' padding as needed.
func EncodeGroup(src []byte, g *[4]byte) {
	var n uint32
	for _, b := range src {
		n = n<<8 | uint32(b)
	}
	n <<= 8 * uint(3-len(src))
	for i := 0; i < 4; i++ {
		g[i] = Char(int(n>>uint(18-6*i)) & 63)
	}
	for i := len(src) + 1; i < 4; i++ {
		g[i] = '='
	}
}

// DecodeGroup decodes one 4-character group into out. It returns the
// number of decoded bytes and whether padding was present (such a group
// must be the last one). On error it also returns the index of the
// offending character inside the group.
func DecodeGroup(g [4]byte) (out [3]byte, n int, padded bool, bad int, err error) {
	var v [4]int
	first := -1
	for i := 0; i < 4; i++ {
		if g[i] == '=' {
			if first < 0 {
				first = i
			}
			continue
		}
		if v[i] = Value(g[i]); v[i] < 0 {
			return out, 0, false, i, ErrInvalidChar
		}
	}
	npad := 0
	if first >= 0 {
		padded = true
		if first < 2 {
			return out, 0, false, first, ErrPadding
		}
		for i := first; i < 4; i++ {
			if g[i] != '=' {
				return out, 0, false, i, ErrPadding
			}
		}
		npad = 4 - first
	}
	if npad == 2 && v[1]&15 != 0 {
		return out, 0, false, 1, ErrNonCanonical
	}
	if npad == 1 && v[2]&3 != 0 {
		return out, 0, false, 2, ErrNonCanonical
	}
	n24 := v[0]<<18 | v[1]<<12 | v[2]<<6 | v[3]
	out[0], out[1], out[2] = byte(n24>>16), byte(n24>>8), byte(n24)
	return out, 3 - npad, padded, -1, nil
}
