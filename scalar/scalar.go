package scalar

// Rune is a Unicode code point candidate (0..0x10FFFF).
type Rune = rune

const (
	MaxRune     rune = 0x10FFFF
	Replacement rune = 0xFFFD
	BOM         rune = 0xFEFF
	SurrogateLo rune = 0xD800
	SurrogateHi rune = 0xDFFF
)

// Valid reports whether r is a Unicode scalar value:
// in range and outside the surrogate area.
func Valid(r rune) bool {
	return r >= 0 && r <= MaxRune && !(r >= SurrogateLo && r <= SurrogateHi)
}

// IsSurrogate reports whether r is a high or low surrogate.
func IsSurrogate(r rune) bool {
	return r >= SurrogateLo && r <= SurrogateHi
}

// IsHighSurrogate reports whether r is a leading surrogate (D800..DBFF).
func IsHighSurrogate(r rune) bool {
	return r >= SurrogateLo && r <= 0xDBFF
}

// IsLowSurrogate reports whether r is a trailing surrogate (DC00..DFFF).
func IsLowSurrogate(r rune) bool {
	return r >= 0xDC00 && r <= SurrogateHi
}

// SurrogatePair decodes a UTF-16 surrogate pair.
func SurrogatePair(high, low rune) rune {
	return 0x10000 + (high-SurrogateLo)<<10 + (low - 0xDC00)
}
