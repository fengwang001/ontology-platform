// Package b64 implements the RFC 4648 standard alphabet and the
// encoding, decoding and strict validity check of a single 4-char group.
package b64

// Alphabet is the RFC 4648 standard Base64 alphabet.
const Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

var rev [256]int8

func init() {
	for i := range rev {
		rev[i] = -1
	}
	for i := 0; i < 64; i++ {
		rev[Alphabet[i]] = int8(i)
	}
}

// Value returns the 6-bit value of c, or -1 if c is not in the alphabet.
func Value(c byte) int { return int(rev[c]) }

// Kind classifies the validity of a 4-character group.
type Kind int

const (
	// OK means the group is a canonical encoding.
	OK Kind = iota
	// BadChar means a character outside the alphabet was found.
	BadChar
	// BadPad means '=' appears in an impossible position.
	BadPad
	// NonCanonical means unused tail bits are not all zero.
	NonCanonical
)

// CheckGroup validates a 4-character group g. It returns the number of
// decoded bytes (1..3), the validity kind, and the index inside g of the
// offending character when kind is not OK.
func CheckGroup(g []byte) (nout int, kind Kind, idx int) {
	var v [4]int
	data := 4
	for i := 0; i < 4; i++ {
		if g[i] == '=' {
			data = i
			break
		}
		v[i] = Value(g[i])
		if v[i] < 0 {
			return 0, BadChar, i
		}
	}
	for i := data; i < 4; i++ {
		if g[i] != '=' {
			return 0, BadPad, i
		}
	}
	switch data {
	case 4:
		return 3, OK, 0
	case 3:
		if v[2]&0x03 != 0 {
			return 0, NonCanonical, 2
		}
		return 2, OK, 0
	case 2:
		if v[1]&0x0F != 0 {
			return 0, NonCanonical, 1
		}
		return 1, OK, 0
	}
	return 0, BadPad, data
}

// Unpack combines the sextets of a valid group into 3 bytes; callers
// keep only the first nout bytes reported by CheckGroup.
func Unpack(g []byte) (b [3]byte) {
	v0, v1 := Value(g[0]), Value(g[1])
	b[0] = byte(v0<<2 | v1>>4)
	if g[2] == '=' {
		return
	}
	v2 := Value(g[2])
	b[1] = byte(v1<<4 | v2>>2)
	if g[3] == '=' {
		return
	}
	b[2] = byte(v2<<6 | Value(g[3]))
	return
}

// AppendEncode appends the 4-character encoding of src (len 1..3) to dst.
func AppendEncode(dst, src []byte) []byte {
	var n uint32
	for _, c := range src {
		n = n<<8 | uint32(c)
	}
	n <<= 8 * uint(3-len(src))
	pad := 3 - len(src)
	for i := 0; i < 4-pad; i++ {
		dst = append(dst, Alphabet[byte(n>>18)&0x3F])
		n <<= 6
	}
	for i := 0; i < pad; i++ {
		dst = append(dst, '=')
	}
	return dst
}
