package b64

import "errors"

const Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

var (
	ErrInvalidChar    = errors.New("b64: invalid base64 character")
	ErrBadPadding     = errors.New("b64: padding is misplaced or malformed")
	ErrNonCanonical   = errors.New("b64: non-canonical trailing bits")
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

func Value(c byte) (byte, bool) {
	v := decodeTable[c]
	return v, v != 0xff
}

func DecodeBlock(block [4]byte) ([]byte, error) {
	var values [4]byte
	for i, c := range block {
		if c == '=' {
			values[i] = 0
			continue
		}
		v, ok := Value(c)
		if !ok {
			return nil, ErrInvalidChar
		}
		values[i] = v
	}

	combined := uint32(values[0])<<18 | uint32(values[1])<<12 |
		uint32(values[2])<<6 | uint32(values[3])
	out := []byte{byte(combined >> 16), byte(combined >> 8), byte(combined)}

	switch {
	case block[2] != '=' && block[3] != '=':
		return out, nil
	case block[2] != '=' && block[3] == '=':
		if values[2]&0x03 != 0 {
			return nil, ErrNonCanonical
		}
		return out[:2], nil
	case block[2] == '=' && block[3] == '=':
		if values[1]&0x0f != 0 {
			return nil, ErrNonCanonical
		}
		return out[:1], nil
	default:
		return nil, ErrBadPadding
	}
}

func EncodeBlock(block []byte) ([4]byte, error) {
	if len(block) < 1 || len(block) > 3 {
		return [4]byte{}, ErrBadPadding
	}

	var padded [3]byte
	copy(padded[:], block)
	combined := uint32(padded[0])<<16 | uint32(padded[1])<<8 | uint32(padded[2])
	encoded := [4]byte{
		Alphabet[(combined>>18)&0x3f],
		Alphabet[(combined>>12)&0x3f],
		Alphabet[(combined>>6)&0x3f],
		Alphabet[combined&0x3f],
	}
	if len(block) == 1 {
		encoded[2] = '='
		encoded[3] = '='
	} else if len(block) == 2 {
		encoded[3] = '='
	}
	return encoded, nil
}
