package eol

const (
	CR = '\r'
	LF = '\n'
)

// Pending reports whether a lone CR is waiting for the next byte.
type Pending bool

// Feed consumes one byte and reports zero or one normalized line ending.
// When b is CR, the caller must hold the byte and feed again after Close.
func (p Pending) Feed(b byte) (next Pending, out []byte, hold bool) {
	if p {
		switch b {
		case LF:
			return false, []byte{LF}, true
		case CR:
			out = []byte{LF}
			return true, out, true
		default:
			out = []byte{LF, b}
			return false, out, true
		}
	}
	if b == CR {
		return true, nil, true
	}
	return false, []byte{b}, false
}

// Finish resolves a held CR as a lone line ending.
func (p Pending) Finish() []byte {
	if p {
		return []byte{LF}
	}
	return nil
}
