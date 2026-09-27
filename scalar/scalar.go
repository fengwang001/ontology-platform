package scalar

type Value rune

const Replacement Value = 0xFFFD

func IsScalar(v Value) bool {
	return false
}

func IsHighSurrogate(v Value) bool {
	return false
}

func IsLowSurrogate(v Value) bool {
	return false
}

func FromSurrogates(high, low Value) Value {
	return Replacement
}
