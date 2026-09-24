// Package b64 implements the RFC 4648 standard Base64 alphabet and the
// strict codec for a single 4-character group.
package b64

import (
	"errors"
	"strconv"
)

// Sentinels classifying group-level failures, always wrapped in *Error.
var (
	ErrChar    = errors.New("b64: invalid character")
	ErrPadding = errors.New("b64: misplaced padding")
	ErrBits    = errors.New("b64: non-canonical trailing bits")
)

// Error pinpoints a group-level failure; Pos is the index within the group.
type Error struct {
	Kind error
	Pos  int
}

func (e *Error) Error() string {
	return e.Kind.Error() + " at group index " + strconv.Itoa(e.Pos)
}

// Unwrap returns the sentinel classifying the failure.
func (e *Error) Unwrap() error { return e.Kind }

const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

var rev [256]int8

func init() {
	for i := range rev {
		rev[i] = -1
	}
	for i := 0; i < len(alphabet); i++ {
		rev[alphabet[i]] = int8(i)
	}
}

// Valid reports whether c belongs to the alphabet (padding excluded).
func Valid(c byte) bool { return rev[c] >= 0 }

// EncodeGroup encodes src (1 to 3 bytes) into dst[0:4], padding with '='.
func EncodeGroup(dst, src []byte) {
	var v uint32
	for _, b := range src {
		v = v<<8 | uint32(b)
	}
	v <<= uint(8 * (3 - len(src)))
	n := len(src) + 1
	for i := 0; i < n; i++ {
		dst[i] = alphabet[v>>uint(18-6*i)&63]
	}
	for i := n; i < 4; i++ {
		dst[i] = '='
	}
}

// DecodeGroup strictly decodes one 4-character group into dst (len >= 3).
// It reports the decoded length and whether the group carries padding,
// which only the final group of a stream may. Trailing bits that no output
// byte uses must be zero, otherwise the group is not canonical.
func DecodeGroup(g, dst []byte) (n int, padded bool, err error) {
	for i := 0; i < 4; i++ {
		if !Valid(g[i]) && g[i] != '=' {
			return 0, false, &Error{ErrChar, i}
		}
	}
	if !Valid(g[0]) || !Valid(g[1]) {
		pos := 0
		if Valid(g[0]) {
			pos = 1
		}
		return 0, false, &Error{ErrPadding, pos}
	}
	v := uint32(rev[g[0]])<<18 | uint32(rev[g[1]])<<12
	switch {
	case g[2] == '=':
		if g[3] != '=' {
			return 0, false, &Error{ErrPadding, 2}
		}
		if rev[g[1]]&0x0f != 0 {
			return 0, false, &Error{ErrBits, 1}
		}
		dst[0] = byte(v >> 16)
		return 1, true, nil
	case g[3] == '=':
		v |= uint32(rev[g[2]]) << 6
		if rev[g[2]]&0x03 != 0 {
			return 0, false, &Error{ErrBits, 2}
		}
		dst[0] = byte(v >> 16)
		dst[1] = byte(v >> 8)
		return 2, true, nil
	default:
		v |= uint32(rev[g[2]])<<6 | uint32(rev[g[3]])
		dst[0] = byte(v >> 16)
		dst[1] = byte(v >> 8)
		dst[2] = byte(v)
		return 3, false, nil
	}
}
