package scalar

const (
	MaxRune = 0
)

func Valid(r rune) bool { return false }

func Surrogate(r rune) bool { return false }

func Pair(r rune) (uint16, uint16, bool) { return 0, 0, false }
