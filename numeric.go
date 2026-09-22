package ontology

import "math"

const (
	rankPositiveFloat   = byte(0x03)
	rankPositiveInteger = byte(0x04)
)

var (
	errTruncated       = &DecodeError{Reason: "truncated byte string"}
	errTruncatedEscape = &DecodeError{Reason: "truncated escape sequence"}
	errInvalidEscape   = &DecodeError{Reason: "invalid escape sequence"}
)

// appendPositiveMagnitude appends a bucketed, variable-length representation.
// Larger representations are lexicographically larger for positive values.
func appendPositiveMagnitude(dst []byte, bits uint64, rank byte) []byte {
	if bits == 0 {
		dst = append(dst, 0x00)
		dst = append(dst, 0x01, rank)
		return append(dst, 0x00)
	}
	biased := int(bits>>52) & 0x7ff
	isSubnormal := biased == 0
	if isSubnormal {
		biased = 1
	}
	exponent := biased - 1023
	dst = appendBucket(dst, exponent)
	significand := bits & (1<<52 - 1)
	significand <<= 8
	if isSubnormal {
		significand <<= 1
	}
	var raw [8]byte
	for index := 7; index >= 0; index-- {
		raw[index] = byte(significand)
		significand >>= 8
	}
	start := 0
	if isSubnormal {
		start = 1
	}
	for start < len(raw)-1 && raw[start] == 0 {
		start++
	}
	end := len(raw)
	for end > start && raw[end-1] == 0 {
		end--
	}
	dst = appendEscaped(dst, raw[start:end])
	dst = append(dst, 0x01, rank)
	return append(dst, 0x00)
}

func appendBucket(dst []byte, exponent int) []byte {
	const minOneByte = -64
	const maxOneByte = 63
	switch {
	case exponent < minOneByte:
		dst = append(dst, 0x40)
		value := uint16(exponent - (-1<<15))
		return append(dst, byte(value>>8), byte(value))
	case exponent <= maxOneByte:
		return append(dst, 0x80+byte(exponent-minOneByte))
	default:
		dst = append(dst, 0xc0)
		value := uint16(exponent - (maxOneByte + 1))
		return append(dst, byte(value>>8), byte(value))
	}
}

func appendFloatBody(dst []byte, value float64) []byte {
	bits := math.Float64bits(value)
	if value < 0 {
		dst = append(dst, 0x00)
		positive := appendPositiveMagnitude(nil, bits&^(uint64(1)<<63), rankPositiveInteger)
		return appendInverted(dst, positive)
	}
	dst = append(dst, 0x01)
	return appendPositiveMagnitude(dst, bits, rankPositiveFloat)
}

func appendIntegerBody(dst []byte, value int64) []byte {
	bits := math.Float64bits(float64(value))
	if value < 0 {
		dst = append(dst, 0x00)
		positive := appendPositiveMagnitude(nil, bits&^(uint64(1)<<63), rankPositiveFloat)
		return appendInverted(dst, positive)
	}
	dst = append(dst, 0x01)
	return appendPositiveMagnitude(dst, bits, rankPositiveInteger)
}

func appendInverted(dst []byte, src []byte) []byte {
	for _, value := range src {
		dst = append(dst, value^0xff)
	}
	return dst
}

func decodeNumericBody(data []byte) (Key, []byte, error) {
	if len(data) == 0 {
		return nil, nil, errTruncated
	}
	negative := data[0] != 0x01
	magnitude := data[1:]
	if negative {
		inverted := make([]byte, len(magnitude))
		for index, value := range magnitude {
			inverted[index] = value ^ 0xff
		}
		magnitude = inverted
	}
	exponent, rest, err := readBucket(magnitude)
	if err != nil {
		return nil, nil, err
	}
	significand, rest, err := readEscaped(rest)
	if err != nil {
		return nil, nil, err
	}
	if exponent == 0 && len(significand) == 0 {
		zero := int64(0)
		if len(rest) > 0 && rest[0] == rankInteger {
			return zero, rest[1:], nil
		}
		return float64(0), rest, nil
	}
	return decodeRankedNumber(exponent, significand, rest, negative)
}

func readBucket(data []byte) (int, []byte, error) {
	if len(data) == 0 {
		return 0, nil, errTruncated
	}
	switch first := data[0]; {
	case first == 0x00:
		return 0, data[1:], nil
	case first < 0x80:
		if len(data) < 3 {
			return 0, nil, errTruncated
		}
		return -1<<15 + int(data[1])<<8 + int(data[2]), data[3:], nil
	case first < 0xc0:
		return -64 + int(first-0x80), data[1:], nil
	default:
		if len(data) < 3 {
			return 0, nil, errTruncated
		}
		return 64 + int(data[1])<<8 + int(data[2]), data[3:], nil
	}
}

func decodeRankedNumber(exponent int, significand, rest []byte, negative bool) (Key, []byte, error) {
	if len(rest) == 0 {
		return nil, nil, errTruncated
	}
	isInteger := rest[0] == rankInteger
	rest = rest[1:]
	if significand[0] == 0 {
		return nil, nil, errInvalidEscape
	}
	var raw [8]byte
	copy(raw[8-len(significand):], significand)
	mantissa := uint64(0)
	for _, value := range raw {
		mantissa = mantissa<<8 | uint64(value)
	}
	mantissa >>= 8
	if exponent < -1022 {
		mantissa >>= 1
	} else {
		mantissa |= 1 << 52
	}
	bits := mantissa | (uint64(exponent+1023) << 52)
	if negative {
		bits |= 1 << 63
	}
	value := math.Float64frombits(bits)
	if isInteger {
		return int64(value), rest, nil
	}
	return value, rest, nil
}
