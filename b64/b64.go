package b64

import "errors"

var (
	ErrInvalidChar      = errors.New("invalid base64 character")
	ErrNonCanonicalTail = errors.New("non-canonical base64 tail")
	ErrPaddingPosition  = errors.New("invalid base64 padding position")
	ErrLength           = errors.New("base64 length is not a multiple of four")
	ErrNewlinePosition  = errors.New("invalid base64 newline position")
)

type GroupError struct {
	Offset int
	Kind   error
}

func (e *GroupError) Error() string { return e.Kind.Error() }
func (e *GroupError) Unwrap() error { return e.Kind }

const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

func EncodeGroup(src []byte) [4]byte {
	var out [4]byte
	out[0] = alphabet[src[0]>>2]
	out[1] = alphabet[(src[0]&0x03)<<4]
	out[2] = '='
	out[3] = '='
	if len(src) > 1 {
		out[1] = alphabet[((src[0]&0x03)<<4)|(src[1]>>4)]
		out[2] = alphabet[(src[1]&0x0f)<<2]
	}
	if len(src) > 2 {
		out[2] = alphabet[((src[1]&0x0f)<<2)|(src[2]>>6)]
		out[3] = alphabet[src[2]&0x3f]
	}
	return out
}

func DecodeGroup(src [4]byte, offset int) ([]byte, error) {
	var vals [4]byte
	for i, char := range src {
		switch {
		case char == '=':
			vals[i] = 0
		case 'A' <= char && char <= 'Z':
			vals[i] = char - 'A'
		case 'a' <= char && char <= 'z':
			vals[i] = char - 'a' + 26
		case '0' <= char && char <= '9':
			vals[i] = char - '0' + 52
		case char == '+':
			vals[i] = 62
		case char == '/':
			vals[i] = 63
		default:
			return nil, &GroupError{Offset: offset + i, Kind: ErrInvalidChar}
		}
	}
	switch {
	case src[0] == '=' || src[1] == '=':
		pos := 0
		if src[1] == '=' {
			pos = 1
		}
		return nil, &GroupError{Offset: offset + pos, Kind: ErrPaddingPosition}
	case src[2] == '=' && src[3] != '=':
		return nil, &GroupError{Offset: offset + 3, Kind: ErrPaddingPosition}
	case src[2] != '=' && src[3] == '=':
		if vals[2]&0x03 != 0 {
			return nil, &GroupError{Offset: offset + 2, Kind: ErrNonCanonicalTail}
		}
		return []byte{vals[0]<<2 | vals[1]>>4, (vals[1]&0x0f)<<4 | vals[2]>>2}, nil
	case src[2] == '=':
		if vals[1]&0x0f != 0 {
			return nil, &GroupError{Offset: offset + 1, Kind: ErrNonCanonicalTail}
		}
		return []byte{vals[0]<<2 | vals[1]>>4}, nil
	default:
		return []byte{
			vals[0]<<2 | vals[1]>>4,
			vals[1]<<4 | vals[2]>>2,
			vals[2]<<6 | vals[3],
		}, nil
	}
}
