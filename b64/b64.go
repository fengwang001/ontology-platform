// Package b64 implements RFC 4648 standard Base64 alphabet handling and
// strict encoding/decoding of a single 4-character group.
package b64

import "errors"

const Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

var decodeTable [256]byte

func init() {
	for i := range decodeTable {
		decodeTable[i] = 0xFF
	}
	for i := 0; i < len(Alphabet); i++ {
		decodeTable[Alphabet[i]] = byte(i)
	}
}

// Sentinel errors classify a bad 4-character group.
var (
	ErrIllegalChar = errors.New("b64: illegal base64 character")
	ErrPadding     = errors.New("b64: padding in illegal position")
	ErrCanonical   = errors.New("b64: non-canonical tail: non-data bits are not zero")
)

// GroupError carries a sentinel Cause plus the offending index within [0,4).
type GroupError struct {
	Cause error
	Index int
}

func (e *GroupError) Error() string { return e.Cause.Error() }
func (e *GroupError) Unwrap() error { return e.Cause }

// EncodeGroup encodes 1-3 bytes into 4 characters. src must be non-empty.
func EncodeGroup(dst []byte, src []byte) {
	v := uint32(src[0]) << 16
	if len(src) > 1 {
		v |= uint32(src[1]) << 8
	}
	if len(src) > 2 {
		v |= uint32(src[2])
	}
	dst[0] = Alphabet[(v>>18)&0x3F]
	dst[1] = Alphabet[(v>>12)&0x3F]
	if len(src) == 1 {
		dst[2], dst[3] = '=', '='
		return
	}
	dst[2] = Alphabet[(v>>6)&0x3F]
	if len(src) == 2 {
		dst[3] = '='
		return
	}
	dst[3] = Alphabet[v&0x3F]
}

// DecodeGroup decodes exactly one 4-character group, enforcing canonical
// padding. It returns the decoded bytes (length 1, 2 or 3).
func DecodeGroup(g [4]byte) ([]byte, error) {
	var v [4]byte
	for i, c := range g {
		if c != '=' {
			v[i] = decodeTable[c]
			if v[i] == 0xFF {
				return nil, &GroupError{Cause: ErrIllegalChar, Index: i}
			}
		}
	}
	switch {
	case g[3] != '=':
		if g[0] == '=' || g[1] == '=' || g[2] == '=' {
			return nil, &GroupError{Cause: ErrPadding, Index: eqIndex(g)}
		}
		return []byte{
			byte(v[0]<<2 | v[1]>>4),
			byte(v[1]<<4 | v[2]>>2),
			byte(v[2]<<6 | v[3]),
		}, nil
	case g[2] == '=':
		if g[0] == '=' || g[1] == '=' {
			return nil, &GroupError{Cause: ErrPadding, Index: eqIndex(g)}
		}
		if v[1]&0x0F != 0 {
			return nil, &GroupError{Cause: ErrCanonical, Index: 1}
		}
		return []byte{byte(v[0]<<2 | v[1]>>4)}, nil
	default:
		if g[0] == '=' || g[1] == '=' {
			return nil, &GroupError{Cause: ErrPadding, Index: eqIndex(g)}
		}
		if v[2]&0x03 != 0 {
			return nil, &GroupError{Cause: ErrCanonical, Index: 2}
		}
		return []byte{
			byte(v[0]<<2 | v[1]>>4),
			byte(v[1]<<4 | v[2]>>2),
		}, nil
	}
}

func eqIndex(g [4]byte) int {
	for i, c := range g {
		if c == '=' {
			return i
		}
	}
	return 0
}
