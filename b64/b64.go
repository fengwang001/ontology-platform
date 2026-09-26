// Package b64 implements standard Base64 (RFC 4648 alphabet) with padding.
// It depends on no other package in this module.
package b64

import "errors"

// Alphabet is the standard alphabet; the byte position is the 6-bit value.
const Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

// Distinct, decidable sentinel errors for every rejection class.
var (
	// ErrLength: total length (including '=') is not a multiple of 4.
	ErrLength = errors.New("b64: encoded length must be a multiple of 4")
	// ErrPadding: '=' appears outside the tail, or there are more than two.
	ErrPadding = errors.New("b64: illegal '=' position or count")
	// ErrChar: a non-'=' byte is outside the alphabet.
	ErrChar = errors.New("b64: illegal base64 character")
	// ErrUnusedBits: trailing unused bits of the final sextet are non-zero.
	ErrUnusedBits = errors.New("b64: trailing unused bits must be zero")
)

const invalidByte = 255

var lookup [256]byte

func init() {
	for i := range lookup {
		lookup[i] = invalidByte
	}
	for i := 0; i < len(Alphabet); i++ {
		lookup[Alphabet[i]] = byte(i)
	}
}

// Encode encodes src into padded standard Base64. Empty input yields "".
func Encode(src []byte) []byte {
	dst := make([]byte, ((len(src)+2)/3)*4)
	si, di := 0, 0
	for ; si+3 <= len(src); si, di = si+3, di+4 {
		encodeBlock(dst[di:di+4], src[si:si+3])
	}
	if si < len(src) {
		encodeBlock(dst[di:di+4], src[si:])
	}
	return dst
}

// encodeBlock maps 1-3 source bytes into exactly 4 destination chars,
// padding with '=' for the 2-byte (one '=') and 1-byte (two '=') tails.
func encodeBlock(dst, src []byte) {
	var b1, b2 byte
	n := len(src)
	b0 := src[0]
	if n > 1 {
		b1 = src[1]
	}
	if n > 2 {
		b2 = src[2]
	}
	v := uint(b0)<<16 | uint(b1)<<8 | uint(b2)
	dst[0] = Alphabet[(v>>18)&63]
	dst[1] = Alphabet[(v>>12)&63]
	switch n {
	case 1:
		dst[2], dst[3] = '=', '='
	case 2:
		dst[2] = Alphabet[(v>>6)&63]
		dst[3] = '='
	default:
		dst[2] = Alphabet[(v>>6)&63]
		dst[3] = Alphabet[v&63]
	}
}

// Decode validates the whole input first and only then produces output, so
// any rejection returns (nil, err) with no partial result.
func Decode(src []byte) ([]byte, error) {
	if len(src) == 0 {
		return []byte{}, nil
	}
	if len(src)%4 != 0 {
		return nil, ErrLength
	}
	pad := 0
	for pad < len(src) && src[len(src)-1-pad] == '=' {
		pad++
	}
	if pad > 2 {
		return nil, ErrPadding
	}
	end := len(src) - pad
	for i := 0; i < end; i++ {
		switch {
		case src[i] == '=':
			return nil, ErrPadding // '=' inside the payload
		case lookup[src[i]] == invalidByte:
			return nil, ErrChar
		}
	}
	groups := len(src) / 4
	dst := make([]byte, 0, groups*3)
	for g := 0; g < groups; g++ {
		p := 0
		if g == groups-1 {
			p = pad
		}
		out, err := decodeBlock(src[g*4:g*4+4], p)
		if err != nil {
			return nil, err
		}
		dst = append(dst, out...)
	}
	return dst, nil
}

// decodeBlock decodes one 4-char block into 3-pad bytes. pad is 0, 1 or 2
// and applies only to the final block; unused tail bits must be zero.
func decodeBlock(block []byte, pad int) ([]byte, error) {
	var v [4]byte
	for i := 0; i < 4; i++ {
		if block[i] == '=' {
			continue
		}
		x := lookup[block[i]]
		if x == invalidByte {
			return nil, ErrChar
		}
		v[i] = x
	}
	switch pad {
	case 1:
		// '=' replaces sextet[3]; the last present sextet is v[2].
		if v[2]&0x03 != 0 {
			return nil, ErrUnusedBits
		}
	case 2:
		// '=' replaces sextets[2:4]; last present sextet is v[1].
		if v[1]&0x0F != 0 {
			return nil, ErrUnusedBits
		}
	}
	u := uint(v[0])<<18 | uint(v[1])<<12 | uint(v[2])<<6 | uint(v[3])
	out := []byte{byte(u >> 16), byte(u >> 8), byte(u)}
	return out[:3-pad], nil
}
