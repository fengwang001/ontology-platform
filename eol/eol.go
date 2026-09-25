// Package eol classifies byte-sized line-ending events.
package eol

type Kind uint8

const (
	Other Kind = iota
	LF
	PendingCR
	CRLF
)

// Inspect combines a previous pending CR with the next byte.
func Inspect(pendingCR bool, b byte) Kind {
	if pendingCR {
		if b == '\n' {
			return CRLF
		}
		return LF
	}
	if b == '\r' {
		return PendingCR
	}
	if b == '\n' {
		return LF
	}
	return Other
}

// IsCR reports whether b is a lone CR candidate.
func IsCR(b byte) bool { return b == '\r' }

// IsLF reports whether b is a line feed.
func IsLF(b byte) bool { return b == '\n' }
