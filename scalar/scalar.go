package scalar

type Value rune

const (
	Replacement      Value = 0xfffd
	ByteOrderMark    Value = 0xfeff
	Max              Value = 0x10ffff
	HighSurrogateMin Value = 0xd800
	HighSurrogateMax Value = 0xdbff
	LowSurrogateMin  Value = 0xdc00
	LowSurrogateMax  Value = 0xdfff
)

func Valid(v Value) bool {
	return v >= 0 && v < HighSurrogateMin || v > LowSurrogateMax && v <= Max
}

func Surrogate(v Value) bool {
	return v >= HighSurrogateMin && v <= LowSurrogateMax
}

func HighSurrogate(v Value) bool {
	return v >= HighSurrogateMin && v <= HighSurrogateMax
}

func LowSurrogate(v Value) bool {
	return v >= LowSurrogateMin && v <= LowSurrogateMax
}

func SurrogatePair(high, low Value) (Value, bool) {
	if !HighSurrogate(high) || !LowSurrogate(low) {
		return 0, false
	}
	return 0x10000 + (high-HighSurrogateMin)<<10 + (low - LowSurrogateMin), true
}

func FromSurrogatePair(high, low uint16) Value {
	return 0x10000 + Value(high-0xd800)<<10 + Value(low-0xdc00)
}
