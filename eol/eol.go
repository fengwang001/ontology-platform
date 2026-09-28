// Package eol recognizes mixed line endings: "\r\n", lone "\r", and "\n".
// A trailing "\r" is pending until the next byte decides CRLF vs lone CR.
package eol

// Kind classifies a byte relative to line-ending recognition.
type Kind uint8

const (
	Other Kind = iota // ordinary byte
	LF                // '\n'
	CR                // '\r'
)

// Classify returns the line-ending class of byte b.
func Classify(b byte) Kind {
	switch b {
	case '\n':
		return LF
	case '\r':
		return CR
	default:
		return Other
	}
}

// Pending tracks whether a "\r" is awaiting the next byte.
// It is a pure state machine with no buffers of its own.
type Pending struct {
	cr bool
}

// Set marks a "\r" as pending.
func (p *Pending) Set() { p.cr = true }

// Clear forgets any pending "\r".
func (p *Pending) Clear() { p.cr = false }

// Active reports whether a "\r" is pending.
func (p *Pending) Active() bool { return p.cr }

// Resolve decides a pending "\r" given the next byte.
// It returns the number of input bytes consumed by the ending:
// 2 for "\r\n", 1 for a lone "\r" (the next byte is left for the caller),
// and 0 when nothing is pending.
func (p *Pending) Resolve(next byte, hasNext bool) int {
	if !p.cr {
		return 0
	}
	p.cr = false
	if hasNext && next == '\n' {
		return 2
	}
	return 1
}
