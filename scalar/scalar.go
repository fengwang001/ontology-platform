package scalar

const (
	MaxRune   = '\U0010FFFF'
	Replacement = '\uFFFD'
)

func Valid(r rune) bool {
	return r >= 0 && r <= MaxRune && !Surrogate(r)
}

func Surrogate(r rune) bool { return r >= 0xD800 && r <= 0xDFFF }

func HighSurrogate(u uint16) bool { return u >= 0xD800 && u <= 0xDBFF }

func LowSurrogate(u uint16) bool { return u >= 0xDC00 && u <= 0xDFFF }
