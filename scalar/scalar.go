package scalar

const (
	Max      = 0x10FFFF
	Replaces = '\uFFFD'
	ZeroBOM  = '\uFEFF'
	surLo    = 0xD800
	surHi    = 0xDFFF
)

type Value rune

func (v Value) Valid() bool {
	return v >= 0 && v <= Max && !v.Surrogate()
}

func (v Value) Surrogate() bool {
	return rune(v) >= surLo && rune(v) <= surHi
}

func HighSurrogate(v Value) bool {
	return rune(v) >= surLo && rune(v) <= 0xDBFF
}

func LowSurrogate(v Value) bool {
	return rune(v) >= 0xDC00 && rune(v) <= surHi
}

func Pair(high, low Value) Value {
	return Value(0x10000 +
		(rune(high)-0xD800)<<10 +
		(rune(low) - 0xDC00))
}

func Surrogates(v Value) (Value, Value) {
	r := rune(v) - 0x10000
	return Value(0xD800 + r>>10), Value(0xDC00 + r&0x3FF)
}
