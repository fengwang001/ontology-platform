package b64

import "errors"

var (
	ErrIllegalCharacter = errors.New("illegal base64 character")
	ErrNonCanonicalTail = errors.New("non-canonical padded tail")
	ErrPaddingPlacement = errors.New("padding is out of place")
	ErrInvalidLength    = errors.New("base64 stream length is not a multiple of four")
	ErrInvalidLineBreak = errors.New("line break is out of place")
)

const Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

var decoded [256]byte

func init() {
	for i := range decoded {
		decoded[i] = 0xff
	}
	for i := 0; i < len(Alphabet); i++ {
		decoded[Alphabet[i]] = byte(i)
	}
}

func EncodeGroup(group []byte) []byte {
	if len(group) < 1 || len(group) > 3 {
		return nil
	}
	out := []byte{'=', '=', '=', '='}
	out[0] = Alphabet[group[0]>>2]
	out[1] = Alphabet[(group[0]<<4)&0x3f]
	if len(group) > 1 {
		out[1] = Alphabet[((group[0]&0x03)<<4)|(group[1]>>4)]
		out[2] = Alphabet[(group[1]<<2)&0x3f]
	}
	if len(group) > 2 {
		out[2] = Alphabet[((group[1]&0x0f)<<2)|(group[2]>>6)]
		out[3] = Alphabet[group[2]&0x3f]
	}
	return out
}

func IsAlphabet(char byte) bool {
	return decoded[char] != 0xff
}

func DecodeChar(char byte) (byte, bool) {
	value := decoded[char]
	return value, value != 0xff
}

func DecodeValues(values [4]byte, padding int) ([]byte, error) {
	switch padding {
	case 1:
		if values[2]&0x03 != 0 {
			return nil, ErrNonCanonicalTail
		}
		return []byte{
			values[0]<<2 | values[1]>>4,
			values[1]<<4 | values[2]>>2,
		}, nil
	case 2:
		if values[1]&0x0f != 0 {
			return nil, ErrNonCanonicalTail
		}
		return []byte{values[0]<<2 | values[1]>>4}, nil
	default:
		return []byte{
			values[0]<<2 | values[1]>>4,
			values[1]<<4 | values[2]>>2,
			values[2]<<6 | values[3],
		}, nil
	}
}

func DecodeGroup(group []byte) ([]byte, error) {
	if len(group) != 4 {
		return nil, ErrInvalidLength
	}

	values := [4]byte{}
	for i, char := range group {
		if char == '=' {
			continue
		}
		value := decoded[char]
		if value == 0xff {
			return nil, ErrIllegalCharacter
		}
		values[i] = value
	}

	padded := 0
	if group[3] == '=' {
		padded = 1
	}
	if group[2] == '=' {
		padded = 2
	}
	if (padded == 1 && group[2] == '=') ||
		(padded == 2 && group[3] != '=') ||
		(padded == 2 && group[1] == '=') ||
		(padded != 2 && group[0] == '=') ||
		(padded == 0 && (group[1] == '=' || group[2] == '=')) {
		return nil, ErrPaddingPlacement
	}

	return DecodeValues(values, padded)
}
