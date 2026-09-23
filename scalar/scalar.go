package scalar

const (
	Replacement Value = 0xFFFD
	MaxRune     Value = 0x10FFFF
)

type Value rune

func IsSurrogate(r Value) bool {
	return r >= 0xD800 && r <= 0xDFFF
}

func IsHighSurrogate(r Value) bool {
	return r >= 0xD800 && r <= 0xDBFF
}

func IsLowSurrogate(r Value) bool {
	return r >= 0xDC00 && r <= 0xDFFF
}

func IsScalar(r Value) bool {
	return r >= 0 && r <= MaxRune && !IsSurrogate(r)
}

func SurrogatePair(high, low Value) Value {
	return 0x10000 + (high-0xD800)<<10 + (low - 0xDC00)
}

func EncodeSurrogatePair(r Value) (Value, Value) {
	r -= 0x10000
	return 0xD800 + (r >> 10), 0xDC00 + r&0x3FF
}
