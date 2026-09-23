package b64

import "errors"

var (
	ErrInvalidCharacter = errors.New("invalid base64 character")
	ErrInvalidPadding   = errors.New("invalid base64 padding")
	ErrNonCanonical     = errors.New("non-canonical base64 tail")
)

const Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

func EncodeGroup(src []byte) (group [4]byte) {
	if len(src) < 1 || len(src) > 3 {
		return group
	}
	group[0] = Alphabet[src[0]>>2]
	group[1] = Alphabet[(src[0]&0x0f)<<4]
	group[2] = '='
	group[3] = '='
	if len(src) == 1 {
		return group
	}
	group[1] = Alphabet[((src[0]&0x0f)<<4)|(src[1]>>4)]
	group[2] = Alphabet[(src[1]&0x03)<<2]
	if len(src) == 2 {
		return group
	}
	group[2] = Alphabet[((src[1]&0x03)<<2)|(src[2]>>6)]
	group[3] = Alphabet[src[2]&0x3f]
	return group
}

func DecodeGroup(group [4]byte) (dst [3]byte, n int, errIndex int, err error) {
	values := [4]byte{}
	for i := 0; i < 2; i++ {
		value, ok := decodeValue(group[i])
		if !ok {
			if group[i] == '=' {
				return dst, 0, i, ErrInvalidPadding
			}
			return dst, 0, i, ErrInvalidCharacter
		}
		values[i] = value
	}

	if group[2] == '=' {
		if group[3] != '=' {
			return dst, 0, 3, ErrInvalidPadding
		}
		if values[1]&0x0f != 0 {
			return dst, 0, 1, ErrNonCanonical
		}
		dst[0] = values[0]<<2 | values[1]>>4
		return dst, 1, -1, nil
	}

	value, ok := decodeValue(group[2])
	if !ok {
		return dst, 0, 2, ErrInvalidCharacter
	}
	values[2] = value
	if group[3] == '=' {
		if values[2]&0x03 != 0 {
			return dst, 0, 2, ErrNonCanonical
		}
		dst[0] = values[0]<<2 | values[1]>>4
		dst[1] = values[1]<<4 | values[2]>>2
		return dst, 2, -1, nil
	}

	value, ok = decodeValue(group[3])
	if !ok {
		if group[3] == '=' {
			return dst, 0, 3, ErrInvalidPadding
		}
		return dst, 0, 3, ErrInvalidCharacter
	}
	values[3] = value

	dst[0] = values[0]<<2 | values[1]>>4
	dst[1] = values[1]<<4 | values[2]>>2
	dst[2] = values[2]<<6 | values[3]
	return dst, 3, -1, nil
}

func decodeValue(char byte) (byte, bool) {
	switch {
	case char >= 'A' && char <= 'Z':
		return char - 'A', true
	case char >= 'a' && char <= 'z':
		return char - 'a' + 26, true
	case char >= '0' && char <= '9':
		return char - '0' + 52, true
	case char == '+':
		return 62, true
	case char == '/':
		return 63, true
	default:
		return 0, false
		}
}
