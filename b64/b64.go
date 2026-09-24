package b64

import "errors"

const (
	Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	Padding  = '='
)

var (
	ErrInvalidCharacter = errors.New("b64: invalid character")
	ErrPaddingPosition  = errors.New("b64: padding in invalid position")
	ErrNonCanonicalTail = errors.New("b64: non-canonical padded tail")
)

func DecodeValue(c byte) (uint, bool) {
	switch {
	case 'A' <= c && c <= 'Z':
		return uint(c - 'A'), true
	case 'a' <= c && c <= 'z':
		return uint(c-'a') + 26, true
	case '0' <= c && c <= '9':
		return uint(c-'0') + 52, true
	case c == '+':
		return 62, true
	case c == '/':
		return 63, true
	default:
		return 0, false
	}
}

func DecodeQuartet(q [4]byte) ([]byte, error) {
	var values [4]uint
	padded := 0
	for i := 0; i < 4; i++ {
		if q[i] == Padding {
			padded++
			continue
		}
		if padded != 0 {
			return nil, ErrPaddingPosition
		}
		value, ok := DecodeValue(q[i])
		if !ok {
			return nil, ErrInvalidCharacter
		}
		values[i] = value
	}

	switch padded {
	case 0:
	case 1:
		if values[2]&3 != 0 {
			return nil, ErrNonCanonicalTail
		}
	case 2:
		if values[1]&15 != 0 {
			return nil, ErrNonCanonicalTail
		}
	default:
		return nil, ErrPaddingPosition
	}

	word := values[0]<<18 | values[1]<<12 | values[2]<<6 | values[3]
	out := []byte{byte(word >> 16), byte(word >> 8), byte(word)}
	return out[:3-padded], nil
}

func IsPadding(q [4]byte) bool {
	return q[2] == Padding || q[3] == Padding
}

func EncodeTriplet(t []byte) [4]byte {
	values := [4]byte{Padding, Padding, Padding, Padding}
	word := uint(0)
	for _, b := range t {
		word = word<<8 | uint(b)
	}
	word <<= uint((3 - len(t)) * 8)
	positions := [4]uint{word >> 18 & 63, word >> 12 & 63, word >> 6 & 63, word & 63}
	for i := 0; i < len(t)+1; i++ {
		values[i] = Alphabet[positions[i]]
	}
	return values
}
