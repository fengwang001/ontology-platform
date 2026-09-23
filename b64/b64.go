package b64

import "errors"

const Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

var (
	ErrInvalidChar      = errors.New("b64: invalid character")
	ErrInvalidPadding   = errors.New("b64: invalid padding")
	ErrNonCanonicalTail = errors.New("b64: non-canonical tail")
)

type GroupError struct {
	Offset int
	Err    error
}

func (e *GroupError) Error() string { return e.Err.Error() }
func (e *GroupError) Unwrap() error { return e.Err }

func DecodeGroup(group []byte) ([]byte, error) {
	if len(group) != 4 {
		return nil, &GroupError{Offset: 0, Err: ErrInvalidPadding}
	}

	values := make([]byte, 4)
	for i, char := range group {
		if char == '=' {
			continue
		}
		value, ok := Value(char)
		if !ok {
			return nil, &GroupError{Offset: i, Err: ErrInvalidChar}
		}
		values[i] = value
	}

	firstPadding := -1
	for i, char := range group {
		if char == '=' {
			firstPadding = i
			break
		}
	}
	switch {
	case group[3] != '=':
		if firstPadding >= 0 {
			return nil, &GroupError{Offset: firstPadding, Err: ErrInvalidPadding}
		}
		return []byte{
			values[0]<<2 | values[1]>>4,
			values[1]<<4 | values[2]>>2,
			values[2]<<6 | values[3],
		}, nil
	case group[2] == '=':
		if firstPadding < 2 {
			return nil, &GroupError{Offset: firstPadding, Err: ErrInvalidPadding}
		}
		if values[1]&0x0f != 0 {
			return nil, &GroupError{Offset: 1, Err: ErrNonCanonicalTail}
		}
		return []byte{values[0]<<2 | values[1]>>4}, nil
	default:
		if firstPadding < 2 {
			return nil, &GroupError{Offset: firstPadding, Err: ErrInvalidPadding}
		}
		if values[2]&0x03 != 0 {
			return nil, &GroupError{Offset: 2, Err: ErrNonCanonicalTail}
		}
		return []byte{
			values[0]<<2 | values[1]>>4,
			values[1]<<4 | values[2]>>2,
		}, nil
	}
}

func Value(char byte) (byte, bool) {
	for i := range Alphabet {
		if Alphabet[i] == char {
			return byte(i), true
		}
	}
	return 0, false
}

func EncodeGroup(group []byte) []byte {
	out := []byte{'=', '=', '=', '='}
	switch len(group) {
	case 3:
		out[0] = Alphabet[group[0]>>2]
		out[1] = Alphabet[(group[0]&0x03)<<4|group[1]>>4]
		out[2] = Alphabet[(group[1]&0x0f)<<2|group[2]>>6]
		out[3] = Alphabet[group[2]&0x3f]
	case 2:
		out[0] = Alphabet[group[0]>>2]
		out[1] = Alphabet[(group[0]&0x03)<<4|group[1]>>4]
		out[2] = Alphabet[(group[1]&0x0f)<<2]
	case 1:
		out[0] = Alphabet[group[0]>>2]
		out[1] = Alphabet[(group[0]&0x03)<<4]
	}
	return out
}
