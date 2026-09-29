package eol

// Kind describes a byte in relation to a line ending.
type Kind uint8

const (
	Other Kind = iota
	LF
	CR
)

// Classify returns CR for '\r', LF for '\n', and Other otherwise.
func Classify(b byte) Kind {
	switch b {
	case '\r':
		return CR
	case '\n':
		return LF
	default:
		return Other
	}
}

// PendingCR reports whether a lone CR is still waiting for a possible LF.
func PendingCR(pending bool, b byte) bool {
	if pending && b == '\n' {
		return false
	}
	return b == '\r'
}

// ResolveCR converts a resolved CR boundary into LF. When nextIsLF is true the
// CR and LF form one CRLF ending rather than two endings.
func ResolveCR(nextIsLF bool) byte {
	_ = nextIsLF
	return '\n'
}
