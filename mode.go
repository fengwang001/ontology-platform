package ontology

type Mode string

const (
	IS  Mode = "IS"
	IX  Mode = "IX"
	S   Mode = "S"
	SIX Mode = "SIX"
	X   Mode = "X"
)

func ValidMode(mode Mode) bool {
	switch mode {
	case IS, IX, S, SIX, X:
		return true
	default:
		return false
	}
}

func ModeLE(left, right Mode) bool {
	if left == right {
		return true
	}
	switch left {
	case IS:
		return right == IX || right == S || right == SIX || right == X
	case IX:
		return right == SIX || right == X
	case S:
		return right == SIX || right == X
	case SIX:
		return right == X
	default:
		return false
	}
}

func JoinMode(left, right Mode) Mode {
	for _, mode := range []Mode{IS, IX, S, SIX, X} {
		if ModeLE(left, mode) && ModeLE(right, mode) {
			return mode
		}
	}
	return ""
}

func CompatibleModes(left, right Mode) bool {
	if left == IS || right == IS {
		return left != X && right != X
	}
	if left == X || right == X {
		return false
	}
	if left == SIX || right == SIX {
		return false
	}
	return left == right || left == IX && right == IX || left == S && right == S
}
