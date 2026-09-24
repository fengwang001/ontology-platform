package b64

import (
	"errors"
	"fmt"
)

const Size = 4

var (
	ErrInvalidChar  = errors.New("invalid base64 character")
	ErrNonCanonical = errors.New("non-canonical base64 tail")
	ErrPadding      = errors.New("misplaced base64 padding")
)

type GroupError struct {
	Kind   error
	Offset int
}

func (e *GroupError) Error() string {
	return fmt.Sprintf("%v at group offset %d", e.Kind, e.Offset)
}

func (e *GroupError) Unwrap() error { return e.Kind }

const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

func valueOf(c byte) (int, bool) {
	switch {
	case 'A' <= c && c <= 'Z':
		return int(c - 'A'), true
	case 'a' <= c && c <= 'z':
		return int(c-'a') + 26, true
	case '0' <= c && c <= '9':
		return int(c-'0') + 52, true
	case c == '+':
		return 62, true
	case c == '/':
		return 63, true
	default:
		return 0, false
	}
}

func EncodeGroup(src []byte, dst []byte) int {
	if len(src) == 0 || len(src) > 3 || len(dst) < Size {
		panic("b64: EncodeGroup requires 1..3 source bytes and 4 destination bytes")
	}
	var block uint32
	for i := 0; i < 3; i++ {
		block <<= 8
		if i < len(src) {
			block |= uint32(src[i])
		}
	}
	dst[0] = alphabet[block>>18&63]
	dst[1] = alphabet[block>>12&63]
	dst[2] = alphabet[block>>6&63]
	dst[3] = alphabet[block&63]
	if len(src) < 3 {
		dst[3] = '='
	}
	if len(src) < 2 {
		dst[2] = '='
	}
	return Size
}

func DecodeGroup(group [Size]byte, out []byte) (int, error) {
	if len(out) < 3 {
		panic("b64: DecodeGroup requires 3 destination bytes")
	}
	var values [Size]int
	firstPad := -1
	for i, c := range group {
		if c == '=' {
			if i < 2 {
				return 0, &GroupError{Kind: ErrPadding, Offset: i}
			}
			if firstPad < 0 {
				firstPad = i
			}
			continue
		}
		v, ok := valueOf(c)
		if !ok {
			return 0, &GroupError{Kind: ErrInvalidChar, Offset: i}
		}
		if firstPad >= 0 {
			return 0, &GroupError{Kind: ErrPadding, Offset: i}
		}
		values[i] = v
	}
	pads := 0
	if firstPad >= 0 {
		pads = Size - firstPad
	}
	switch pads {
	case 2:
		if values[1]&15 != 0 {
			return 0, &GroupError{Kind: ErrNonCanonical, Offset: 1}
		}
	case 1:
		if values[2]&3 != 0 {
			return 0, &GroupError{ErrNonCanonical, 2}
		}
	}
	block := uint32(values[0])<<18 | uint32(values[1])<<12 | uint32(values[2])<<6 | uint32(values[3])
	out[0] = byte(block >> 16)
	out[1] = byte(block >> 8)
	out[2] = byte(block)
	return 3 - pads, nil
}
