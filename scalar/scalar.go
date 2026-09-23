package scalar

type Kind uint8

const (
	Invalid Kind = iota
	Scalar
	HighSurrogate
	LowSurrogate
)

func Classify(r rune) Kind { return Invalid }

func Valid(r rune) bool { return false }

func Surrogate(r rune) bool { return false }
