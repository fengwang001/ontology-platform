// Package b64 implements strict RFC 4648 Base64 groups.
package b64

import "errors"

const Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

type Kind uint8

const (
	KindInvalidCharacter Kind = iota + 1
	KindNonCanonical
	KindPadding
	KindLength
	KindLineBreak
)

var (
	ErrInvalidCharacter = errors.New("b64: invalid character")
	ErrNonCanonical     = errors.New("b64: non-canonical tail")
	ErrPadding          = errors.New("b64: padding position error")
	ErrLength           = errors.New("b64: input length is not a multiple of four")
	ErrLineBreak        = errors.New("b64: line-break position error")
)

type Error struct {
	Kind   Kind
	Offset int64
}

func (e *Error) Error() string { return e.sentinel().Error() }
func (e *Error) Unwrap() error { return e.sentinel() }

func (e *Error) sentinel() error {
	switch e.Kind {
	case KindInvalidCharacter:
		return ErrInvalidCharacter
	case KindNonCanonical:
		return ErrNonCanonical
	case KindPadding:
		return ErrPadding
	case KindLength:
		return ErrLength
	default:
		return ErrLineBreak
	}
}

func NewError(kind Kind, offset int64) *Error { return &Error{Kind: kind, Offset: offset} }

var decodeTable = func() [256]int8 {
	var table [256]int8
	for i := range table {
		table[i] = -1
	}
	for i := range Alphabet {
		table[Alphabet[i]] = int8(i)
	}
	return table
}()

func DecodeValue(b byte) (int, bool) {
	v := decodeTable[b]
	return int(v), v >= 0
}

func ValidGroup(group []byte) bool {
	_, err := DecodeGroup(group)
	return err == nil
}

func EncodeGroup(dst, src []byte) int {
	if len(src) == 0 || len(dst) < 4 {
		return 0
	}
	n := len(src)
	if n > 3 {
		n = 3
}
	block := uint32(src[0]) << 16
	if n > 1 {
		block |= uint32(src[n-2]) << 8
	}
	if n > 2 {
		block |= uint32(src[n-1])
	}
	dst[0] = Alphabet[block>>18&0x3f]
	dst[1] = Alphabet[block>>12&0x3f]
	dst[2], dst[3] = '=', '='
	if n > 1 {
		dst[2] = Alphabet[block>>6&0x3f]
	}
	if n > 2 {
		dst[3] = Alphabet[block&0x3f]
	}
	return 4
}

func DecodeGroup(group []byte) ([]byte, error) {
	if len(group) != 4 {
		return nil, NewError(KindLength, int64(len(group)))
	}
	values := [4]int{}
	for i, b := range group {
		if b == '=' {
			values[i] = -2
			continue
		}
		v, ok := DecodeValue(b)
		if !ok {
			return nil, NewError(KindInvalidCharacter, int64(i))
		}
		values[i] = v
	}
	block := uint32(values[0]&0x3f)<<18 | uint32(values[1]&0x3f)<<12
	switch {
	case values[0] >= 0 && values[1] >= 0 && values[2] >= 0 && values[3] >= 0:
		block |= uint32(values[2])<<6 | uint32(values[3])
		return []byte{byte(block >> 16), byte(block >> 8), byte(block)}, nil
	case values[0] >= 0 && values[1] >= 0 && values[2] >= 0 && values[3] == -2:
		if values[2]&0x3 != 0 {
			return nil, NewError(KindNonCanonical, 2)
		}
		block |= uint32(values[2]) << 6
		return []byte{byte(block >> 16), byte(block >> 8)}, nil
	case values[0] >= 0 && values[1] >= 0 && values[2] == -2 && values[3] == -2:
		if values[1]&0xf != 0 {
			return nil, NewError(KindNonCanonical, 1)
		}
		return []byte{byte(block >> 16)}, nil
	default:
		padded := false
		for i, v := range values {
			if v == -2 {
				padded = true
			}
			if padded && v >= 0 || !padded && v == -2 && i != 0 && values[i-1] < 0 {
				return nil, NewError(KindPadding, int64(i))
			}
		}
		return nil, NewError(KindPadding, 0)
	}
}
