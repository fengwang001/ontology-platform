// Package eol identifies line endings (\r\n, lone \r, \n) and the
// undecided state of a trailing \r at a stream/chunk boundary.
package eol

// Kind classifies a byte boundary outcome.
type Kind uint8

const (
	Other   Kind = iota // ordinary content byte
	EndLF               // a \n ending a line (possibly part of \r\n)
	EndCR               // a lone \r ending a line
	Pending             // a trailing \r whose following byte is unknown
)

// CR and LF byte values.
const (
	CR byte = '\r'
	LF byte = '\n'
)

// IsCR / IsLF report byte identity.
func IsCR(b byte) bool { return b == CR }
func IsLF(b byte) bool { return b == LF }

// Pair reports whether the two bytes form a CRLF sequence.
func Pair(a, b byte) bool { return a == CR && b == LF }

// Classify interprets a byte given whether the previous unconsumed byte was a
// pending \r. n is the number of input bytes consumed (0, 1, or 2):
// when pending and b is \n, the pair is one LF ending.
// Otherwise \r alone yields CR, \n yields LF, anything else Other.
// A returned Pending means b is \r and must be withheld until the next byte.
func Classify(pending bool, b byte) (k Kind, n int) {
	switch {
	case pending && b == LF:
		return EndLF, 1 // consumes only the \n; the held \r pairs with it
	case pending:
		return EndCR, 0 // held \r settles as a lone ending; b not consumed
	case b == CR:
		return Pending, 0
	case b == LF:
		return EndLF, 1
	default:
		return Other, 1
	}
}

// Flush settles a still-held \r at end of stream: CR when held, else Other.
func Flush(pending bool) Kind {
	if pending {
		return EndCR
	}
	return Other
}
