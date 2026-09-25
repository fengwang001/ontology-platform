package scalar

const (
	Replacement = '\uFFFD'
	MaxRune     = '\U0010FFFF'
)

func Valid(r rune) bool {
	return false
}

func Surrogate(r rune) bool {
	return false
}
