// Package b64 implements the RFC 4648 alphabet and one 4-character group.
package b64

// Alphabet is the standard Base64 alphabet.
const Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

// Padding is the RFC 4648 padding character.
const Padding = '='

// Group is one decoded 24-bit unit with zero, one, or two present input bytes.
type Group struct {
	// Bytes contains the 1-3 decoded bytes. It is empty only for empty input.
	Bytes []byte
	// PaddingCount is 0, 1, or 2 trailing '=' characters in the group.
	PaddingCount int
}

var decodeValues [256]int8

func init() {
	for index := range decodeValues {
		decodeValues[index] = -1
	}
	for index, symbol := range []byte(Alphabet) {
		decodeValues[symbol] = int8(index)
	}
}

// Value reports the 6-bit value represented by one alphabet symbol.
// It returns false for padding and every other byte.
func Value(symbol byte) (uint8, bool) {
	value := decodeValues[symbol]
	if value < 0 {
		return 0, false
	}
	return uint8(value), true
}

// EncodeGroup encodes up to three bytes into exactly four symbols, including
// canonical padding. data must contain one to three bytes.
func EncodeGroup(data []byte) [4]byte {
	var output [4]byte
	triple := uint32(data[0]) << 16
	if len(data) > 1 {
		triple |= uint32(data[1]) << 8
	}
	if len(data) > 2 {
		triple |= uint32(data[2])
	}

	values := [4]uint8{
		uint8(triple >> 18),
		uint8(triple >> 12 & 0x3f),
		uint8(triple >> 6 & 0x3f),
		uint8(triple & 0x3f),
	}
	for index, value := range values {
		output[index] = Alphabet[value]
	}

	switch len(data) {
	case 1:
		output[2], output[3] = Padding, Padding
	case 2:
		output[3] = Padding
	}
	return output
}

// DecodeGroup decodes exactly four symbols. The values must all contain
// alphabet symbols or padding; the caller is responsible for classifying
// invalid characters and enforcing padding placement.
func DecodeGroup(symbols [4]byte, paddingCount int) Group {
	var values [4]uint8
	for index := 0; index < 4-paddingCount; index++ {
		values[index], _ = Value(symbols[index])
	}

	triple := uint32(values[0])<<18 | uint32(values[1])<<12 |
		uint32(values[2])<<6 | uint32(values[3])
	switch paddingCount {
	case 1:
		return Group{
			Bytes:        []byte{byte(triple >> 16), byte(triple >> 8)},
			PaddingCount: 1,
		}
	case 2:
		return Group{
			Bytes:        []byte{byte(triple >> 16)},
			PaddingCount: 2,
		}
	default:
		return Group{
			Bytes: []byte{
				byte(triple >> 16),
				byte(triple >> 8),
				byte(triple),
			},
		}
	}
}

// Canonical reports whether the non-padding symbol values have the zero tail
// bits required for the indicated padding count.
func Canonical(values [4]uint8, paddingCount int) bool {
	switch paddingCount {
	case 1:
		return values[2]&0x03 == 0
	case 2:
		return values[1]&0x0f == 0
	default:
		return true
	}
}
