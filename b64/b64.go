// Package b64 implements RFC 4648 standard-alphabet Base64 at the level of a
// single four-character quantum, including strict canonical-tail checks.
package b64

import "errors"

// Alphabet is the RFC 4648 standard alphabet.
const Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

// Sentinel errors: the five failure classes are mutually distinct (errors.Is).
var (
	ErrIllegalChar  = errors.New("b64: illegal character")
	ErrNonCanonical = errors.New("b64: non-canonical tail")
	ErrPadding      = errors.New("b64: invalid padding placement")
	ErrLength       = errors.New("b64: input length is not a multiple of 4")
	ErrNewline      = errors.New("b64: newline at invalid position")
)

// Quantum is one four-character group and the 1-3 bytes it carries.
type Quantum struct {
	Chars [4]byte
	Data  [3]byte
	N     int  // payload bytes: 3, 2 or 1
	Final bool // true when the quantum carries padding (N < 3)
}

var decodeTable [256]byte

func init() {
	for i := range decodeTable {
		decodeTable[i] = 0xFF
	}
	for i := 0; i < 64; i++ {
		decodeTable[Alphabet[i]] = byte(i)
	}
}

// Value returns the 6-bit value of an alphabet byte and ok=false otherwise.
func Value(c byte) (byte, bool) {
	v := decodeTable[c]
	return v, v != 0xFF
}

// EncodeQuantum encodes one to three bytes into a four-character quantum.
// n must be between 1 and 3.
func EncodeQuantum(src []byte, n int) Quantum {
	var q Quantum
	q.N = n
	q.Data[0] = src[0]
	if n >= 2 {
		q.Data[1] = src[1]
	}
	if n >= 3 {
		q.Data[2] = src[2]
	}
	v0 := src[0] >> 2
	v1 := (src[0] & 0x03) << 4
	q.Chars[0] = Alphabet[v0]
	if n == 1 {
		q.Chars[1], q.Chars[2], q.Chars[3] = Alphabet[v1], '=', '='
		q.Final = true
		return q
	}

	v1 |= src[1] >> 4
	q.Chars[1] = Alphabet[v1]
	if n == 2 {
		q.Chars[2], q.Chars[3] = '=', '='
		q.Final = true
		return q
	}
	v2 := (src[1] & 0x0F) << 2
	v2 |= src[2] >> 6
	q.Chars[2] = Alphabet[v2]
	q.Chars[3] = Alphabet[src[2]&0x3F]
	return q
}

// DecodeQuantum decodes exactly four bytes. Padding is validated for position
// and canonicality; it returns one of the five sentinel errors on failure.
func DecodeQuantum(chars [4]byte) (Quantum, error) {
	var q Quantum
	q.Chars = chars
	pads := 0
	for i := 0; i < 4; i++ {
		if chars[i] == '=' {
			pads++
		}
	}
	var vals [4]byte
	switch pads {
	case 0:
		for i := 0; i < 4; i++ {
			v, ok := Value(chars[i])
			if !ok {
				return q, illegal(chars[i])
			}
			vals[i] = v
		}
	case 1:
		if chars[3] != '=' {
			return q, ErrPadding
		}
		for i := 0; i < 3; i++ {
			v, ok := Value(chars[i])
			if !ok {
				return q, illegal(chars[i])
			}
			vals[i] = v
		}
		if vals[2]&0x03 != 0 {
			return q, ErrNonCanonical
		}
	case 2:
		if chars[2] != '=' || chars[3] != '=' {
			return q, ErrPadding
		}
		for i := 0; i < 2; i++ {
			v, ok := Value(chars[i])
			if !ok {
				return q, illegal(chars[i])
			}
			vals[i] = v
		}
		if vals[1]&0x0F != 0 {
			return q, ErrNonCanonical
		}
	default:
		return q, ErrPadding
	}
	q.N = 3 - pads
	q.Final = pads > 0
	q.Data[0] = vals[0]<<2 | vals[1]>>4
	if q.N >= 2 {
		q.Data[1] = vals[1]<<4 | vals[2]>>2
	}
	if q.N >= 3 {
		q.Data[2] = vals[2]<<6 | vals[3]
	}
	return q, nil
}

func illegal(c byte) error {
	if c == '\r' || c == '\n' {
		return ErrNewline
	}
	return ErrIllegalChar
}
