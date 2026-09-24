package scalar

const MaxRune = '\U0010FFFF'

func Surrogate(r rune) bool { return false }

func Valid(r rune) bool { return false }
