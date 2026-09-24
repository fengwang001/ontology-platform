package b64

import "errors"

const Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

var (
	ErrInvalidCharacter = errors.New("b64: invalid character")
	ErrPaddingPosition  = errors.New("b64: padding is not in the final position")
	ErrNonCanonicalTail = errors.New("b64: non-canonical tail")
)

var decodeTable [256]byte

func init() {
	for i := range decodeTable {
		decodeTable[i] = 0xff
	}
	for i := 0; i < len(Alphabet); i++ {
		decodeTable[Alphabet[i]] = byte(i)
	}
}

func ValidChar(char byte) bool {
	return decodeTable[char] != 0xff
}

func EncodeChunk(in []byte) (group [4]byte, count int) {
	copy(group[:], "====")
	count = len(in)
	if count == 0 {
		return group, 0
	}
	group[0] = Alphabet[in[0]>>2]
	if count == 1 {
		group[1] = Alphabet[(in[0]&0x03)<<4]
		return group, 1
	}
	group[1] = Alphabet[((in[0]&0x03)<<4)|(in[1]>>4)]
	if count == 2 {
		group[2] = Alphabet[(in[1]&0x0f)<<2]
		return group, 2
	}
	group[2] = Alphabet[((in[1]&0x0f)<<2)|(in[2]>>6)]
	group[3] = Alphabet[in[2]&0x3f]
	return group, 3
}

func DecodeChunk(group [4]byte) (out [3]byte, count int, err error) {
	var values [4]byte
	for i, char := range group {
		if char == '=' {
			values[i] = 0
			continue
		}
		value := decodeTable[char]
		if value == 0xff {
			return out, 0, ErrInvalidCharacter
		}
		values[i] = value
	}

	switch {
	case group[0] == '=' || group[1] == '=' || (group[2] == '=' && group[3] != '='):
		return out, 0, ErrPaddingPosition
	case group[2] == '=' && group[3] == '=' && values[1]&0x0f != 0:
		return out, 0, ErrNonCanonicalTail
	case group[2] != '=' && group[3] == '=' && values[2]&0x03 != 0:
		return out, 0, ErrNonCanonicalTail
	}

	out[0] = values[0]<<2 | values[1]>>4
	if group[2] == '=' {
		return out, 1, nil
	}
	out[1] = values[1]<<4 | values[2]>>2
	if group[3] == '=' {
		return out, 2, nil
	}
	out[2] = values[2]<<6 | values[3]
	return out, 3, nil
}
