package scalar

type Value rune

const maxValue Value = 0x10FFFF

const Replacement Value = 0xFFFD

func Valid(v Value) bool {
	return uint32(v) <= uint32(maxValue) && !Surrogate(v)
}

func Surrogate(v Value) bool {
	return uint32(v)-0xD800 < 0x800
}

func HighSurrogate(v Value) bool {
	return uint32(v)-0xD800 < 0x400
}

func LowSurrogate(v Value) bool {
	return uint32(v)-0xDC00 < 0x400
}

func SurrogatePair(high, low Value) (Value, bool) {
	if !HighSurrogate(high) || !LowSurrogate(low) {
		return Replacement, false
	}
	return 0x10000 + (high-0xD800)<<10 + (low - 0xDC00), true
}
