package eol

type Kind byte

const (
	None Kind = iota
	CR
	LF
	CRLF
)

func (k Kind) String() string {
	switch k {
	case CR:
		return "CR"
	case LF:
		return "LF"
	case CRLF:
		return "CRLF"
	default:
		return "None"
	}
}

// Decode examines the byte at p[0]. pending reports whether a previously
// buffered CR is still undecided because this byte is LF.
func Decode(p []byte, crPending bool) (kind Kind, size int, pending bool) {
	if len(p) == 0 {
		return None, 0, crPending
	}
	switch p[0] {
	case '\r':
		if len(p) == 1 {
			return None, 0, true
		}
		if p[1] == '\n' {
			return CRLF, 2, false
		}
		return CR, 1, false
	case '\n':
		if crPending {
			return CRLF, 1, false
		}
		return LF, 1, false
	default:
		return None, 1, crPending
	}
}
